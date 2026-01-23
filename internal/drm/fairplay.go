package drm

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FairPlayConfig contains FairPlay-specific configuration.
type FairPlayConfig struct {
	LicenseURL  string `json:"licenseUrl"`  // Key Server Module (KSM) URL
	Certificate string `json:"certificate"` // Base64 encoded FPS certificate
	ASK         string `json:"ask"`         // Application Secret Key (hex)
}

// FairPlayProvider implements FairPlay DRM key provisioning.
type FairPlayProvider struct {
	config      *FairPlayConfig
	client      *http.Client
	certificate []byte
	ask         []byte
}

// FairPlay HLS key format constants
const (
	FairPlayKeyFormat         = "com.apple.streamingkeydelivery"
	FairPlayKeyFormatVersions = "1"
)

// NewFairPlayProvider creates a new FairPlay provider.
func NewFairPlayProvider(cfg *FairPlayConfig) (*FairPlayProvider, error) {
	if cfg.LicenseURL == "" {
		return nil, fmt.Errorf("FairPlay license URL is required")
	}

	provider := &FairPlayProvider{
		config: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Decode certificate if provided
	if cfg.Certificate != "" {
		cert, err := base64.StdEncoding.DecodeString(cfg.Certificate)
		if err != nil {
			return nil, fmt.Errorf("invalid FairPlay certificate: %w", err)
		}
		provider.certificate = cert
	}

	// Decode ASK if provided
	if cfg.ASK != "" {
		ask, err := hexDecode(cfg.ASK)
		if err != nil {
			return nil, fmt.Errorf("invalid FairPlay ASK: %w", err)
		}
		provider.ask = ask
	}

	return provider, nil
}

// GetSystemConfig returns the FairPlay DRM system configuration.
func (p *FairPlayProvider) GetSystemConfig(ctx context.Context, key *ContentKey) (*DRMSystemConfig, error) {
	_, span := tracer.Start(ctx, "fairplay-get-system-config")
	defer span.End()

	// Generate the key URI for HLS playlist
	keyURI := fmt.Sprintf("%s?kid=%s", p.config.LicenseURL, key.KeyID)

	return &DRMSystemConfig{
		System:            SystemFairPlay,
		SystemID:          SystemIDFairPlay,
		LicenseURL:        keyURI,
		KeyFormat:         FairPlayKeyFormat,
		KeyFormatVersions: FairPlayKeyFormatVersions,
		Certificate:       p.config.Certificate,
	}, nil
}

// GenerateHLSKeyTag generates the EXT-X-KEY tag for FairPlay in HLS playlists.
func (p *FairPlayProvider) GenerateHLSKeyTag(key *ContentKey) string {
	keyURI := fmt.Sprintf("%s?kid=%s", p.config.LicenseURL, key.KeyID)

	return fmt.Sprintf(
		`#EXT-X-KEY:METHOD=SAMPLE-AES,URI="%s",KEYFORMAT="%s",KEYFORMATVERSIONS="%s"`,
		keyURI,
		FairPlayKeyFormat,
		FairPlayKeyFormatVersions,
	)
}

// GetCertificate retrieves the FairPlay Streaming certificate.
func (p *FairPlayProvider) GetCertificate(ctx context.Context) ([]byte, error) {
	if p.certificate != nil {
		return p.certificate, nil
	}

	// If no cached certificate, fetch from URL
	certURL := p.config.LicenseURL + "/cert"
	
	req, err := http.NewRequestWithContext(ctx, "GET", certURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("certificate request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("certificate request failed with status %d", resp.StatusCode)
	}

	cert, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate: %w", err)
	}

	p.certificate = cert
	return cert, nil
}

// ProcessSPCRequest processes a Server Playback Context (SPC) request.
func (p *FairPlayProvider) ProcessSPCRequest(ctx context.Context, spc []byte) ([]byte, error) {
	ctx, span := tracer.Start(ctx, "fairplay-process-spc")
	defer span.End()

	req, err := http.NewRequestWithContext(ctx, "POST", p.config.LicenseURL, bytes.NewReader(spc))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SPC request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SPC request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

// EncryptContentKey encrypts a content key with the ASK for FairPlay.
func (p *FairPlayProvider) EncryptContentKey(contentKey []byte) ([]byte, error) {
	if len(p.ask) != 16 {
		return nil, fmt.Errorf("invalid ASK length: expected 16 bytes, got %d", len(p.ask))
	}

	if len(contentKey) != 16 {
		return nil, fmt.Errorf("invalid content key length: expected 16 bytes, got %d", len(contentKey))
	}

	block, err := aes.NewCipher(p.ask)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// ECB mode encryption (FairPlay uses this for key wrapping)
	encrypted := make([]byte, 16)
	block.Encrypt(encrypted, contentKey)

	return encrypted, nil
}

// GenerateCKCResponse generates a Content Key Context (CKC) response.
// This is a simplified version - production would use proper FPS protocols.
func (p *FairPlayProvider) GenerateCKCResponse(key *ContentKey) ([]byte, error) {
	keyBytes, err := hexDecode(key.Key)
	if err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}

	ivBytes, err := hexDecode(key.IV)
	if err != nil {
		return nil, fmt.Errorf("invalid IV: %w", err)
	}

	// In production, this would generate a properly formatted CKC
	// For now, return a simple structure
	var ckc bytes.Buffer
	ckc.Write(keyBytes)
	ckc.Write(ivBytes)

	return ckc.Bytes(), nil
}

// DecryptSPC decrypts a Server Playback Context using the ASK.
func (p *FairPlayProvider) DecryptSPC(spc []byte) ([]byte, error) {
	if len(p.ask) != 16 {
		return nil, fmt.Errorf("ASK not configured")
	}

	block, err := aes.NewCipher(p.ask)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	// SPC is encrypted with CBC mode
	if len(spc) < aes.BlockSize {
		return nil, fmt.Errorf("SPC too short")
	}

	iv := spc[:aes.BlockSize]
	ciphertext := spc[aes.BlockSize:]

	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext is not a multiple of block size")
	}

	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove PKCS7 padding
	return removePKCS7Padding(plaintext)
}

func removePKCS7Padding(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}

	padding := int(data[len(data)-1])
	if padding > len(data) || padding > aes.BlockSize {
		return nil, fmt.Errorf("invalid padding")
	}

	for i := len(data) - padding; i < len(data); i++ {
		if data[i] != byte(padding) {
			return nil, fmt.Errorf("invalid padding bytes")
		}
	}

	return data[:len(data)-padding], nil
}

