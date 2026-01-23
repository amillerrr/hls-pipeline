package transcoder

import (
	"fmt"
	"os"
	"path/filepath"
)

// DRMEncryptionConfig contains configuration for DRM encryption during transcoding.
type DRMEncryptionConfig struct {
	// Enabled indicates whether encryption is enabled
	Enabled bool `json:"enabled"`

	// Scheme is the encryption scheme (cenc or cbcs)
	Scheme string `json:"scheme"`

	// KeyID is the encryption key ID (32 hex chars)
	KeyID string `json:"keyId"`

	// Key is the encryption key (32 hex chars)
	Key string `json:"key"`

	// IV is the initialization vector (32 hex chars, optional)
	IV string `json:"iv,omitempty"`

	// KeyURI is the URI for HLS EXT-X-KEY tag
	KeyURI string `json:"keyUri"`

	// KeyFormat is the HLS key format (e.g., com.apple.streamingkeydelivery for FairPlay)
	KeyFormat string `json:"keyFormat,omitempty"`

	// KeyFormatVersions is the HLS key format versions
	KeyFormatVersions string `json:"keyFormatVersions,omitempty"`

	// WidevinePSSH is the base64-encoded Widevine PSSH box
	WidevinePSSH string `json:"widevinePssh,omitempty"`

	// PlayReadyPSSH is the base64-encoded PlayReady PSSH box
	PlayReadyPSSH string `json:"playreadyPssh,omitempty"`
}

// GenerateKeyInfoFile generates an HLS encryption key info file for FFmpeg.
// The key info file contains:
// Line 1: Key URI (for playlist)
// Line 2: Key file path
// Line 3: IV (optional)
func (d *DRMEncryptionConfig) GenerateKeyInfoFile(outputDir string) (string, error) {
	if !d.Enabled {
		return "", nil
	}

	// Write the key to a file
	keyFilePath := filepath.Join(outputDir, "encryption.key")
	keyBytes, err := hexToBytes(d.Key)
	if err != nil {
		return "", fmt.Errorf("invalid key format: %w", err)
	}
	if err := os.WriteFile(keyFilePath, keyBytes, 0600); err != nil {
		return "", fmt.Errorf("failed to write key file: %w", err)
	}

	// Write the key info file
	keyInfoPath := filepath.Join(outputDir, "key_info.txt")

	content := d.KeyURI + "\n" + keyFilePath
	if d.IV != "" {
		content += "\n" + d.IV
	}

	if err := os.WriteFile(keyInfoPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write key info file: %w", err)
	}

	return keyInfoPath, nil
}

// GetHLSEncryptionMethod returns the HLS encryption method for this config.
func (d *DRMEncryptionConfig) GetHLSEncryptionMethod() string {
	if d.KeyFormat == "com.apple.streamingkeydelivery" {
		return "SAMPLE-AES"
	}

	switch d.Scheme {
	case "cbcs":
		return "SAMPLE-AES"
	case "cenc":
		return "SAMPLE-AES-CTR"
	default:
		return "AES-128"
	}
}

// GetFFmpegEncryptionArgs returns FFmpeg arguments for HLS encryption.
func (d *DRMEncryptionConfig) GetFFmpegEncryptionArgs(keyInfoPath string) []string {
	return []string{
		"-hls_key_info_file", keyInfoPath,
	}
}

// GetShakaPackagerArgs returns Shaka Packager arguments for encryption.
func (d *DRMEncryptionConfig) GetShakaPackagerArgs() []string {
	args := []string{
		"--enable_raw_key_encryption",
		"--keys", fmt.Sprintf("key_id=%s:key=%s", d.KeyID, d.Key),
		"--protection_scheme", d.Scheme,
	}

	if d.IV != "" {
		args = append(args, "--iv", d.IV)
	}

	// Protection systems
	var systems []string
	if d.WidevinePSSH != "" {
		systems = append(systems, "Widevine")
		args = append(args, "--pssh", d.WidevinePSSH)
	}
	if d.PlayReadyPSSH != "" {
		systems = append(systems, "PlayReady")
	}
	if d.KeyFormat == "com.apple.streamingkeydelivery" {
		systems = append(systems, "FairPlay")
		if d.KeyURI != "" {
			args = append(args, "--hls_key_uri", d.KeyURI)
		}
	}

	if len(systems) > 0 {
		args = append(args, "--protection_systems", joinStrings(systems, ","))
	}

	return args
}

// Helper functions

func hexToBytes(hex string) ([]byte, error) {
	if len(hex)%2 != 0 {
		return nil, fmt.Errorf("hex string has odd length")
	}

	result := make([]byte, len(hex)/2)
	for i := 0; i < len(hex); i += 2 {
		var b byte
		_, err := fmt.Sscanf(hex[i:i+2], "%02x", &b)
		if err != nil {
			return nil, fmt.Errorf("invalid hex at position %d: %w", i, err)
		}
		result[i/2] = b
	}

	return result, nil
}

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
