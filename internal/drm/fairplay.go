package drm

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FairPlayConfig contains FairPlay-specific configuration.
type FairPlayConfig struct {
	CertificateURL string `json:"certificateUrl"`
	LicenseURL     string `json:"licenseUrl"`
	KeyServerURL   string `json:"keyServerUrl"`
	IV             string `json:"iv,omitempty"` // 16-byte hex string
}

// FairPlayKeyInfo contains FairPlay key information.
type FairPlayKeyInfo struct {
	KeyID         string `json:"keyId"`
	Key           string `json:"key"`
	IV            string `json:"iv"`
	KeyURI        string `json:"keyUri"`
	CertificateB64 string `json:"certificate,omitempty"`
}

// FairPlayClient handles FairPlay DRM operations.
type FairPlayClient struct {
	config     *FairPlayConfig
	httpClient *http.Client
	certCache  []byte
}

// NewFairPlayClient creates a new FairPlay client.
func NewFairPlayClient(cfg *FairPlayConfig) *FairPlayClient {
	return &FairPlayClient{
		config: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// FetchCertificate fetches the FairPlay Streaming certificate.
func (c *FairPlayClient) FetchCertificate(ctx context.Context) ([]byte, error) {
	if c.certCache != nil {
		return c.certCache, nil
	}

	if c.config.CertificateURL == "" {
		return nil, fmt.Errorf("FairPlay certificate URL not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.config.CertificateURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch certificate: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("certificate fetch failed with status %d", resp.StatusCode)
	}

	cert, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate: %w", err)
	}

	c.certCache = cert
	return cert, nil
}

// GenerateFairPlayKeyInfo generates FairPlay key information.
func (c *FairPlayClient) GenerateFairPlayKeyInfo(keyID, key string) (*FairPlayKeyInfo, error) {
	if err := ValidateKIDFormat(keyID); err != nil {
		return nil, fmt.Errorf("invalid key ID: %w", err)
	}
	if err := ValidateKeyFormat(key); err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}

	// Generate or use configured IV
	iv := c.config.IV
	if iv == "" {
		// Generate random IV (in production, use crypto/rand)
		iv = generateDeterministicHex(keyID, "fairplay-iv", 16)
	}

	info := &FairPlayKeyInfo{
		KeyID:  keyID,
		Key:    key,
		IV:     iv,
		KeyURI: c.buildKeyURI(keyID),
	}

	return info, nil
}

// buildKeyURI builds the key URI for HLS playlists.
func (c *FairPlayClient) buildKeyURI(keyID string) string {
	if c.config.KeyServerURL != "" {
		return fmt.Sprintf("%s?kid=%s", c.config.KeyServerURL, keyID)
	}
	// Use skd:// URI scheme for FairPlay
	return fmt.Sprintf("skd://%s", keyID)
}

// GenerateHLSKeyTag generates the EXT-X-KEY tag for FairPlay.
func GenerateHLSKeyTag(keyInfo *FairPlayKeyInfo, method string) string {
	if method == "" {
		method = "SAMPLE-AES" // Default for FairPlay with CMAF
	}

	// Format IV as 0x-prefixed hex string
	ivHex := keyInfo.IV
	if len(ivHex) == 32 {
		ivHex = "0x" + ivHex
	}

	return fmt.Sprintf(
		`#EXT-X-KEY:METHOD=%s,URI="%s",KEYFORMAT="com.apple.streamingkeydelivery",KEYFORMATVERSIONS="1",IV=%s`,
		method,
		keyInfo.KeyURI,
		ivHex,
	)
}

// GenerateHLSSessionKeyTag generates the EXT-X-SESSION-KEY tag for FairPlay.
func GenerateHLSSessionKeyTag(keyInfo *FairPlayKeyInfo) string {
	ivHex := "0x" + keyInfo.IV
	
	return fmt.Sprintf(
		`#EXT-X-SESSION-KEY:METHOD=SAMPLE-AES,URI="%s",KEYFORMAT="com.apple.streamingkeydelivery",KEYFORMATVERSIONS="1",IV=%s`,
		keyInfo.KeyURI,
		ivHex,
	)
}

// GetFairPlayPackagerArgs returns Shaka Packager arguments for FairPlay encryption.
func GetFairPlayPackagerArgs(keyInfo *FairPlayKeyInfo) []string {
	args := []string{
		"--protection_scheme", "cbcs",
		"--protection_systems", "FairPlay",
		"--hls_key_uri", keyInfo.KeyURI,
	}

	if keyInfo.IV != "" {
		args = append(args, "--hls_iv", keyInfo.IV)
	}

	return args
}

// FairPlayContentProtectionData generates the content protection data for HLS.
func FairPlayContentProtectionData(keyID string) string {
	// Convert key ID to base64 for HLS signaling
	kidBytes, _ := hex.DecodeString(keyID)
	return base64.StdEncoding.EncodeToString(kidBytes)
}

// ValidateFairPlayConfig validates FairPlay configuration.
func ValidateFairPlayConfig(cfg *FairPlayConfig) error {
	if cfg.KeyServerURL == "" && cfg.LicenseURL == "" {
		return fmt.Errorf("either keyServerUrl or licenseUrl is required for FairPlay")
	}

	if cfg.IV != "" {
		if len(cfg.IV) != 32 {
			return fmt.Errorf("IV must be 32 hex characters (16 bytes)")
		}
		if err := ValidateKeyFormat(cfg.IV); err != nil {
			return fmt.Errorf("invalid IV format: %w", err)
		}
	}

	return nil
}

// FairPlayEncryptionInfo contains all information needed for FairPlay encryption.
type FairPlayEncryptionInfo struct {
	KeyID          string `json:"keyId"`
	Key            string `json:"key"`
	IV             string `json:"iv"`
	KeyURI         string `json:"keyUri"`
	KeyFormat      string `json:"keyFormat"`
	KeyFormatVersions string `json:"keyFormatVersions"`
	Method         string `json:"method"`
}

// NewFairPlayEncryptionInfo creates FairPlay encryption info with defaults.
func NewFairPlayEncryptionInfo(keyID, key, iv, keyURI string) *FairPlayEncryptionInfo {
	return &FairPlayEncryptionInfo{
		KeyID:             keyID,
		Key:               key,
		IV:                iv,
		KeyURI:            keyURI,
		KeyFormat:         "com.apple.streamingkeydelivery",
		KeyFormatVersions: "1",
		Method:            "SAMPLE-AES",
	}
}

// ToHLSTag converts the encryption info to an HLS EXT-X-KEY tag.
func (e *FairPlayEncryptionInfo) ToHLSTag() string {
	return fmt.Sprintf(
		`#EXT-X-KEY:METHOD=%s,URI="%s",KEYFORMAT="%s",KEYFORMATVERSIONS="%s",IV=0x%s`,
		e.Method,
		e.KeyURI,
		e.KeyFormat,
		e.KeyFormatVersions,
		e.IV,
	)
}

