package drm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WidevineConfig contains Widevine-specific configuration.
type WidevineConfig struct {
	LicenseURL string `json:"licenseUrl"`
	SignerKey  string `json:"signerKey"` // Base64 encoded
	SignerIV   string `json:"signerIv"`  // Base64 encoded
	Provider   string `json:"provider"`
	ContentID  string `json:"contentId"`
}

// WidevineProvider implements Widevine DRM key provisioning.
type WidevineProvider struct {
	config *WidevineConfig
	client *http.Client
}

// NewWidevineProvider creates a new Widevine provider.
func NewWidevineProvider(cfg *WidevineConfig) (*WidevineProvider, error) {
	if cfg.LicenseURL == "" {
		return nil, fmt.Errorf("Widevine license URL is required")
	}

	return &WidevineProvider{
		config: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// GetSystemConfig returns the Widevine DRM system configuration.
func (p *WidevineProvider) GetSystemConfig(ctx context.Context, key *ContentKey) (*DRMSystemConfig, error) {
	_, span := tracer.Start(ctx, "widevine-get-system-config")
	defer span.End()

	// Generate PSSH box for Widevine
	pssh, err := p.generatePSSH(key)
	if err != nil {
		return nil, fmt.Errorf("failed to generate Widevine PSSH: %w", err)
	}

	return &DRMSystemConfig{
		System:     SystemWidevine,
		SystemID:   SystemIDWidevine,
		LicenseURL: p.config.LicenseURL,
		PSSH:       pssh,
	}, nil
}

// generatePSSH generates a Widevine PSSH box.
func (p *WidevineProvider) generatePSSH(key *ContentKey) (string, error) {
	// Widevine PSSH format:
	// - 4 bytes: box size
	// - 4 bytes: "pssh"
	// - 1 byte: version (0 or 1)
	// - 3 bytes: flags
	// - 16 bytes: system ID
	// - 4 bytes: data size (version 0) or key ID count (version 1)
	// - variable: data or key IDs

	keyIDBytes, err := hexDecode(key.KeyID)
	if err != nil {
		return "", fmt.Errorf("invalid key ID: %w", err)
	}

	// Simple PSSH with just the key ID
	// This is a minimal PSSH - in production, you'd include provider-specific data
	systemID, _ := hexDecode(NormalizeKeyID(SystemIDWidevine))

	var pssh bytes.Buffer

	// Version 1 PSSH with key ID
	dataSize := 4 + 16 // 4 bytes key count + 16 bytes key ID
	boxSize := 12 + 16 + 4 + dataSize

	// Box size
	writeUint32BE(&pssh, uint32(boxSize))
	// Box type
	pssh.WriteString("pssh")
	// Version and flags
	pssh.WriteByte(1) // Version 1
	pssh.Write([]byte{0, 0, 0}) // Flags
	// System ID
	pssh.Write(systemID)
	// Key ID count
	writeUint32BE(&pssh, 1)
	// Key ID
	pssh.Write(keyIDBytes)
	// Data size (no extra data)
	writeUint32BE(&pssh, 0)

	return base64.StdEncoding.EncodeToString(pssh.Bytes()), nil
}

// GetLicense makes a license request to the Widevine license server.
func (p *WidevineProvider) GetLicense(ctx context.Context, request []byte) ([]byte, error) {
	ctx, span := tracer.Start(ctx, "widevine-get-license")
	defer span.End()

	req, err := http.NewRequestWithContext(ctx, "POST", p.config.LicenseURL, bytes.NewReader(request))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("license request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("license request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

// WidevineLicenseRequest represents a Widevine license request.
type WidevineLicenseRequest struct {
	ContentID string `json:"content_id"`
	Policy    string `json:"policy,omitempty"`
	Tracks    []struct {
		Type string `json:"type"`
	} `json:"tracks,omitempty"`
}

// CreateLicenseRequest creates a Widevine license request for key provisioning.
func (p *WidevineProvider) CreateLicenseRequest(contentID string) ([]byte, error) {
	req := WidevineLicenseRequest{
		ContentID: contentID,
		Tracks: []struct {
			Type string `json:"type"`
		}{
			{Type: "SD"},
			{Type: "HD"},
			{Type: "AUDIO"},
		},
	}

	return json.Marshal(req)
}

// Helper functions

func hexDecode(s string) ([]byte, error) {
	// Handle both with and without dashes
	s = NormalizeKeyID(s)
	
	result := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		var b byte
		_, err := fmt.Sscanf(s[i:i+2], "%02x", &b)
		if err != nil {
			return nil, err
		}
		result[i/2] = b
	}
	return result, nil
}

func writeUint32BE(buf *bytes.Buffer, v uint32) {
	buf.WriteByte(byte(v >> 24))
	buf.WriteByte(byte(v >> 16))
	buf.WriteByte(byte(v >> 8))
	buf.WriteByte(byte(v))
}

