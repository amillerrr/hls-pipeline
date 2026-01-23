package drm

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("drm-provider")

// DRM System IDs (as per DASH-IF)
const (
	WidevineSystemID  = "edef8ba9-79d6-4ace-a3c8-27dcd51d21ed"
	FairPlaySystemID  = "94ce86fb-07ff-4f43-adb8-93d2fa968ca2"
	PlayReadySystemID = "9a04f079-9840-4286-ab92-e65be0885f95"
)

// ContentKey represents a DRM content key.
type ContentKey struct {
	// KeyID is the key identifier (UUID format).
	KeyID string `json:"keyId"`

	// Key is the encryption key (hex or base64 encoded).
	Key string `json:"key"`

	// IV is the initialization vector (optional, for FairPlay).
	IV string `json:"iv,omitempty"`

	// ExplicitIV indicates if IV should be signaled explicitly.
	ExplicitIV bool `json:"explicitIv,omitempty"`
}

// DRMSystem represents a single DRM system configuration.
type DRMSystem struct {
	// SystemID is the DRM system identifier.
	SystemID string `json:"systemId"`

	// PSSH is the Protection System Specific Header (base64).
	PSSH string `json:"pssh,omitempty"`

	// LicenseURL is the URL for license acquisition.
	LicenseURL string `json:"licenseUrl"`

	// KeyFormat is the key format for HLS (e.g., "identity", "urn:uuid:...").
	KeyFormat string `json:"keyFormat,omitempty"`

	// KeyFormatVersions is the key format versions.
	KeyFormatVersions string `json:"keyFormatVersions,omitempty"`

	// Certificate is the DRM certificate (for FairPlay).
	Certificate string `json:"certificate,omitempty"`
}

// KeyProvider defines the interface for DRM key providers.
type KeyProvider interface {
	// GetContentKeys returns content keys for a video.
	GetContentKeys(ctx context.Context, videoID string) (*ContentKey, error)

	// GetDRMSystems returns DRM system configurations.
	GetDRMSystems(ctx context.Context, videoID string) ([]DRMSystem, error)

	// GetLicenseURL returns the license acquisition URL for a DRM system.
	GetLicenseURL(ctx context.Context, systemID string) (string, error)

	// ProxyLicenseRequest proxies a license request to the DRM provider.
	ProxyLicenseRequest(ctx context.Context, systemID string, request []byte) ([]byte, error)
}

// ExternalKeyProvider implements KeyProvider using an external CPIX API.
type ExternalKeyProvider struct {
	cpixEndpoint string
	apiKey       string
	httpClient   *http.Client
	logger       *slog.Logger
	licenseURLs  map[string]string
}

// ExternalKeyProviderConfig contains configuration for external DRM providers.
type ExternalKeyProviderConfig struct {
	// CPIXEndpoint is the CPIX key exchange endpoint.
	CPIXEndpoint string `json:"cpixEndpoint"`

	// APIKey is the API key for authentication.
	APIKey string `json:"apiKey"`

	// WidevineLicenseURL is the Widevine license server URL.
	WidevineLicenseURL string `json:"widevineLicenseUrl"`

	// FairPlayLicenseURL is the FairPlay license server URL.
	FairPlayLicenseURL string `json:"fairplayLicenseUrl"`

	// PlayReadyLicenseURL is the PlayReady license server URL.
	PlayReadyLicenseURL string `json:"playreadyLicenseUrl"`

	// Timeout is the HTTP request timeout.
	Timeout time.Duration `json:"timeout"`
}

// NewExternalKeyProvider creates a new external key provider.
func NewExternalKeyProvider(config *ExternalKeyProviderConfig, logger *slog.Logger) *ExternalKeyProvider {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	return &ExternalKeyProvider{
		cpixEndpoint: config.CPIXEndpoint,
		apiKey:       config.APIKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger: logger,
		licenseURLs: map[string]string{
			WidevineSystemID:  config.WidevineLicenseURL,
			FairPlaySystemID:  config.FairPlayLicenseURL,
			PlayReadySystemID: config.PlayReadyLicenseURL,
		},
	}
}

// GetContentKeys fetches content keys from the CPIX endpoint.
func (p *ExternalKeyProvider) GetContentKeys(ctx context.Context, videoID string) (*ContentKey, error) {
	ctx, span := tracer.Start(ctx, "drm-get-content-keys",
		trace.WithAttributes(
			attribute.String("video.id", videoID),
		))
	defer span.End()

	// Build CPIX request
	cpixRequest := buildCPIXRequest(videoID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cpixEndpoint, strings.NewReader(cpixRequest))
	if err != nil {
		return nil, fmt.Errorf("failed to create CPIX request: %w", err)
	}

	req.Header.Set("Content-Type", "application/xml")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("CPIX request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CPIX request returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read CPIX response: %w", err)
	}

	// Parse CPIX response
	cpixDoc, err := parseCPIXDocument(body)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to parse CPIX response: %w", err)
	}

	if len(cpixDoc.ContentKeys) == 0 {
		return nil, fmt.Errorf("no content keys in CPIX response")
	}

	key := cpixDoc.ContentKeys[0]
	return &ContentKey{
		KeyID: key.KeyID,
		Key:   key.Data.Secret.PlainValue,
		IV:    key.Data.Secret.IV,
	}, nil
}

// GetDRMSystems returns DRM system configurations.
func (p *ExternalKeyProvider) GetDRMSystems(ctx context.Context, videoID string) ([]DRMSystem, error) {
	_, span := tracer.Start(ctx, "drm-get-systems",
		trace.WithAttributes(
			attribute.String("video.id", videoID),
		))
	defer span.End()

	systems := []DRMSystem{
		{
			SystemID:          WidevineSystemID,
			LicenseURL:        p.licenseURLs[WidevineSystemID],
			KeyFormat:         "urn:uuid:" + WidevineSystemID,
			KeyFormatVersions: "1",
		},
		{
			SystemID:          FairPlaySystemID,
			LicenseURL:        p.licenseURLs[FairPlaySystemID],
			KeyFormat:         "com.apple.streamingkeydelivery",
			KeyFormatVersions: "1",
		},
		{
			SystemID:          PlayReadySystemID,
			LicenseURL:        p.licenseURLs[PlayReadySystemID],
			KeyFormat:         "urn:uuid:" + PlayReadySystemID,
			KeyFormatVersions: "1",
		},
	}

	return systems, nil
}

// GetLicenseURL returns the license URL for a DRM system.
func (p *ExternalKeyProvider) GetLicenseURL(ctx context.Context, systemID string) (string, error) {
	if url, ok := p.licenseURLs[systemID]; ok && url != "" {
		return url, nil
	}
	return "", fmt.Errorf("no license URL configured for system %s", systemID)
}

// ProxyLicenseRequest proxies a license request to the DRM provider.
func (p *ExternalKeyProvider) ProxyLicenseRequest(ctx context.Context, systemID string, request []byte) ([]byte, error) {
	ctx, span := tracer.Start(ctx, "drm-proxy-license",
		trace.WithAttributes(
			attribute.String("drm.system_id", systemID),
			attribute.Int("request.size", len(request)),
		))
	defer span.End()

	licenseURL, err := p.GetLicenseURL(ctx, systemID)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, licenseURL, bytes.NewReader(request))
	if err != nil {
		return nil, fmt.Errorf("failed to create license request: %w", err)
	}

	// Set appropriate content type based on DRM system
	switch systemID {
	case WidevineSystemID:
		req.Header.Set("Content-Type", "application/octet-stream")
	case PlayReadySystemID:
		req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	case FairPlaySystemID:
		req.Header.Set("Content-Type", "application/octet-stream")
	}

	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("license request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("license request returned status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// LocalKeyProvider implements KeyProvider using local key generation.
// This is primarily for development and testing.
type LocalKeyProvider struct {
	keys      map[string]*ContentKey
	logger    *slog.Logger
	staticKey bool
}

// NewLocalKeyProvider creates a new local key provider.
func NewLocalKeyProvider(logger *slog.Logger, staticKey bool) *LocalKeyProvider {
	return &LocalKeyProvider{
		keys:      make(map[string]*ContentKey),
		logger:    logger,
		staticKey: staticKey,
	}
}

// GetContentKeys generates or retrieves content keys.
func (p *LocalKeyProvider) GetContentKeys(ctx context.Context, videoID string) (*ContentKey, error) {
	_, span := tracer.Start(ctx, "local-get-content-keys")
	defer span.End()

	// Check cache
	if key, ok := p.keys[videoID]; ok {
		return key, nil
	}

	// Generate new key
	var keyID, key, iv []byte

	if p.staticKey {
		// Use deterministic key for testing
		keyID = []byte(videoID)
		if len(keyID) < 16 {
			keyID = append(keyID, make([]byte, 16-len(keyID))...)
		}
		keyID = keyID[:16]
		key = []byte("0123456789abcdef") // Static test key
		iv = []byte("abcdef0123456789")  // Static test IV
	} else {
		// Generate random key
		keyID = make([]byte, 16)
		if _, err := rand.Read(keyID); err != nil {
			return nil, fmt.Errorf("failed to generate key ID: %w", err)
		}

		key = make([]byte, 16) // AES-128
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("failed to generate key: %w", err)
		}

		iv = make([]byte, 16)
		if _, err := rand.Read(iv); err != nil {
			return nil, fmt.Errorf("failed to generate IV: %w", err)
		}
	}

	contentKey := &ContentKey{
		KeyID: hex.EncodeToString(keyID),
		Key:   hex.EncodeToString(key),
		IV:    hex.EncodeToString(iv),
	}

	p.keys[videoID] = contentKey

	p.logger.InfoContext(ctx, "Generated local content key",
		"videoId", videoID,
		"keyId", contentKey.KeyID,
	)

	return contentKey, nil
}

// GetDRMSystems returns DRM system configurations for local testing.
func (p *LocalKeyProvider) GetDRMSystems(ctx context.Context, videoID string) ([]DRMSystem, error) {
	// For local testing, return minimal configurations
	return []DRMSystem{
		{
			SystemID:   "clear-key",
			LicenseURL: "/drm/license/clearkey",
			KeyFormat:  "identity",
		},
	}, nil
}

// GetLicenseURL returns an empty URL for local testing.
func (p *LocalKeyProvider) GetLicenseURL(ctx context.Context, systemID string) (string, error) {
	return "/drm/license/" + systemID, nil
}

// ProxyLicenseRequest handles license requests locally.
func (p *LocalKeyProvider) ProxyLicenseRequest(ctx context.Context, systemID string, request []byte) ([]byte, error) {
	// For ClearKey, return the key directly
	if systemID == "clear-key" {
		// Parse ClearKey request (simplified)
		// In production, properly parse the JSON request
		return []byte(`{"keys":[],"type":"temporary"}`), nil
	}

	return nil, fmt.Errorf("local license proxy not supported for %s", systemID)
}

// CPIX Document structures

// CPIXDocument represents a CPIX 2.3 document.
type CPIXDocument struct {
	XMLName     xml.Name         `xml:"CPIX"`
	ContentID   string           `xml:"contentId,attr"`
	ContentKeys []CPIXContentKey `xml:"ContentKeyList>ContentKey"`
	DRMSystems  []CPIXDRMSystem  `xml:"DRMSystemList>DRMSystem"`
}

// CPIXContentKey represents a content key in CPIX.
type CPIXContentKey struct {
	KeyID        string     `xml:"kid,attr"`
	CommonKey    string     `xml:"commonEncryptionScheme,attr,omitempty"`
	Data         CPIXData   `xml:"Data"`
	ExplicitIV   string     `xml:"explicitIV,attr,omitempty"`
}

// CPIXData contains the key data.
type CPIXData struct {
	Secret CPIXSecret `xml:"Secret>PlainValue"`
}

// CPIXSecret contains the actual key value.
type CPIXSecret struct {
	PlainValue string `xml:",chardata"`
	IV         string `xml:"iv,attr,omitempty"`
}

// CPIXDRMSystem represents a DRM system in CPIX.
type CPIXDRMSystem struct {
	SystemID   string `xml:"systemId,attr"`
	KeyID      string `xml:"kid,attr"`
	PSSH       string `xml:"PSSH,omitempty"`
	ContentKey string `xml:"ContentProtectionData,omitempty"`
	URIExtXKey string `xml:"URIExtXKey,omitempty"`
	HLSSignalingData *CPIXHLSSignaling `xml:"HLSSignalingData,omitempty"`
}

// CPIXHLSSignaling contains HLS-specific signaling data.
type CPIXHLSSignaling struct {
	MasterPlaylist string `xml:"masterPlaylist,attr,omitempty"`
	MediaPlaylist  string `xml:"mediaPlaylist,attr,omitempty"`
}

// buildCPIXRequest builds a CPIX request document.
func buildCPIXRequest(contentID string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<CPIX xmlns="urn:dashif:org:cpix" xmlns:pskc="urn:ietf:params:xml:ns:keyprov:pskc" 
      xmlns:speke="urn:aws:amazon:com:speke" contentId="%s" version="2.3">
  <ContentKeyList>
    <ContentKey kid="%s" commonEncryptionScheme="cbcs"/>
  </ContentKeyList>
  <DRMSystemList>
    <DRMSystem kid="%s" systemId="%s"/>
    <DRMSystem kid="%s" systemId="%s"/>
    <DRMSystem kid="%s" systemId="%s"/>
  </DRMSystemList>
</CPIX>`,
		contentID,
		generateKeyID(contentID),
		generateKeyID(contentID), WidevineSystemID,
		generateKeyID(contentID), FairPlaySystemID,
		generateKeyID(contentID), PlayReadySystemID,
	)
}

// parseCPIXDocument parses a CPIX response document.
func parseCPIXDocument(data []byte) (*CPIXDocument, error) {
	var doc CPIXDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal CPIX: %w", err)
	}
	return &doc, nil
}

// generateKeyID generates a deterministic key ID from content ID.
func generateKeyID(contentID string) string {
	// Create a deterministic UUID from content ID
	h := make([]byte, 16)
	copy(h, []byte(contentID))

	return fmt.Sprintf("%x-%x-%x-%x-%x",
		h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// GenerateHLSKeyTags generates EXT-X-KEY tags for HLS.
func GenerateHLSKeyTags(key *ContentKey, systems []DRMSystem) string {
	var tags strings.Builder

	for _, sys := range systems {
		switch sys.SystemID {
		case FairPlaySystemID:
			tags.WriteString(fmt.Sprintf(
				"#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"%s\",KEYFORMAT=\"%s\",KEYFORMATVERSIONS=\"%s\"\n",
				sys.LicenseURL+"?kid="+key.KeyID,
				sys.KeyFormat,
				sys.KeyFormatVersions,
			))

		case WidevineSystemID, PlayReadySystemID:
			tags.WriteString(fmt.Sprintf(
				"#EXT-X-KEY:METHOD=SAMPLE-AES-CTR,URI=\"%s\",KEYID=0x%s,KEYFORMAT=\"%s\",KEYFORMATVERSIONS=\"%s\"\n",
				sys.LicenseURL,
				key.KeyID,
				sys.KeyFormat,
				sys.KeyFormatVersions,
			))
		}
	}

	return tags.String()
}

// GeneratePSSH generates a PSSH box for a DRM system.
func GeneratePSSH(systemID string, keyID string, data []byte) (string, error) {
	// PSSH box structure (simplified)
	systemIDBytes, err := parseSystemID(systemID)
	if err != nil {
		return "", err
	}

	keyIDBytes, err := hex.DecodeString(strings.ReplaceAll(keyID, "-", ""))
	if err != nil {
		return "", fmt.Errorf("invalid key ID: %w", err)
	}

	// Build PSSH box
	psshData := make([]byte, 0, 32+len(data))
	psshData = append(psshData, systemIDBytes...)
	psshData = append(psshData, keyIDBytes...)
	psshData = append(psshData, data...)

	return base64.StdEncoding.EncodeToString(psshData), nil
}

// parseSystemID parses a system ID string to bytes.
func parseSystemID(systemID string) ([]byte, error) {
	clean := strings.ReplaceAll(systemID, "-", "")
	return hex.DecodeString(clean)
}

// EncryptSegment encrypts a segment using AES-128-CBC.
func EncryptSegment(data []byte, key []byte, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// PKCS7 padding
	padding := aes.BlockSize - (len(data) % aes.BlockSize)
	padded := make([]byte, len(data)+padding)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(padding)
	}

	// Encrypt
	encrypted := make([]byte, len(padded))
	mode := NewCBCEncrypter(block, iv)
	mode.CryptBlocks(encrypted, padded)

	return encrypted, nil
}

// CBCEncrypter is a simple CBC mode encrypter.
type CBCEncrypter struct {
	b  cipher
	iv []byte
}

type cipher interface {
	BlockSize() int
	Encrypt(dst, src []byte)
}

// NewCBCEncrypter creates a new CBC encrypter.
func NewCBCEncrypter(b cipher, iv []byte) *CBCEncrypter {
	return &CBCEncrypter{b: b, iv: iv}
}

// CryptBlocks encrypts blocks in CBC mode.
func (c *CBCEncrypter) CryptBlocks(dst, src []byte) {
	blockSize := c.b.BlockSize()
	iv := c.iv

	for len(src) > 0 {
		// XOR with IV
		for i := 0; i < blockSize; i++ {
			dst[i] = src[i] ^ iv[i]
		}

		// Encrypt
		c.b.Encrypt(dst[:blockSize], dst[:blockSize])

		// Next IV is the ciphertext
		iv = dst[:blockSize]

		src = src[blockSize:]
		dst = dst[blockSize:]
	}
}
