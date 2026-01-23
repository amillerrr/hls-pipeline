package drm

import (
	"context"
	"fmt"
)

// System IDs (DASH-IF / CENC UUIDs)
const (
	// SystemIDWidevine is the Widevine DRM system ID.
	SystemIDWidevine = "edef8ba9-79d6-4ace-a3c8-27dcd51d21ed"

	// SystemIDFairPlay is the FairPlay DRM system ID.
	SystemIDFairPlay = "94ce86fb-07ff-4f43-adb8-93d2fa968ca2"

	// SystemIDPlayReady is the PlayReady DRM system ID.
	SystemIDPlayReady = "9a04f079-9840-4286-ab92-e65be0885f95"

	// SystemIDClearKey is the ClearKey DRM system ID.
	SystemIDClearKey = "1077efec-c0b2-4d02-ace3-3c1e52e2fb4b"
)

// DRMSystem represents a DRM system type.
type DRMSystem string

const (
	// SystemWidevine represents Widevine DRM.
	SystemWidevine DRMSystem = "widevine"

	// SystemFairPlay represents FairPlay DRM.
	SystemFairPlay DRMSystem = "fairplay"

	// SystemPlayReady represents PlayReady DRM.
	SystemPlayReady DRMSystem = "playready"

	// SystemClearKey represents ClearKey DRM.
	SystemClearKey DRMSystem = "clearkey"
)

// EncryptionScheme represents a CENC encryption scheme.
type EncryptionScheme string

const (
	// EncryptionSchemeCENC is AES-CTR mode encryption (Widevine, PlayReady).
	EncryptionSchemeCENC EncryptionScheme = "cenc"

	// EncryptionSchemeCBCS is AES-CBC with pattern encryption (FairPlay, also supported by Widevine/PlayReady).
	EncryptionSchemeCBCS EncryptionScheme = "cbcs"
)

// ContentKey represents a DRM content encryption key.
type ContentKey struct {
	// KeyID is the key identifier (UUID format, 32 hex chars without dashes).
	KeyID string `json:"keyId"`

	// Key is the encryption key (32 hex chars = 16 bytes).
	Key string `json:"key"`

	// IV is the initialization vector (32 hex chars, optional for FairPlay).
	IV string `json:"iv,omitempty"`

	// ExplicitIV indicates whether the IV should be signaled explicitly.
	ExplicitIV bool `json:"explicitIv,omitempty"`
}

// DRMSystemConfig represents configuration for a single DRM system.
type DRMSystemConfig struct {
	// System is the DRM system type.
	System DRMSystem `json:"system"`

	// SystemID is the DASH-IF system ID (UUID format with dashes).
	SystemID string `json:"systemId"`

	// PSSH is the Protection System Specific Header (base64 encoded).
	PSSH string `json:"pssh,omitempty"`

	// LicenseURL is the license acquisition URL.
	LicenseURL string `json:"licenseUrl"`

	// KeyFormat is the HLS key format (for FairPlay).
	KeyFormat string `json:"keyFormat,omitempty"`

	// KeyFormatVersions is the HLS key format versions.
	KeyFormatVersions string `json:"keyFormatVersions,omitempty"`

	// Certificate is the DRM certificate (base64, for FairPlay).
	Certificate string `json:"certificate,omitempty"`
}

// EncryptionConfig contains the complete DRM encryption configuration.
type EncryptionConfig struct {
	// Enabled indicates whether DRM is enabled.
	Enabled bool `json:"enabled"`

	// Scheme is the encryption scheme (cenc or cbcs).
	Scheme EncryptionScheme `json:"scheme"`

	// Keys contains the content encryption keys.
	Keys []ContentKey `json:"keys"`

	// Systems contains the DRM system configurations.
	Systems []DRMSystemConfig `json:"systems"`

	// KeyRotation indicates whether key rotation is enabled.
	KeyRotation bool `json:"keyRotation,omitempty"`

	// KeyRotationPeriod is the key rotation period in seconds.
	KeyRotationPeriod int `json:"keyRotationPeriod,omitempty"`
}

// KeyProvider is the interface for DRM key providers.
type KeyProvider interface {
	// GetContentKey returns a content key for the given video ID.
	GetContentKey(ctx context.Context, videoID string) (*ContentKey, error)

	// GetDRMSystems returns DRM system configurations for the given video ID.
	GetDRMSystems(ctx context.Context, videoID string) ([]DRMSystemConfig, error)

	// GetEncryptionConfig returns the complete encryption configuration.
	GetEncryptionConfig(ctx context.Context, videoID string) (*EncryptionConfig, error)
}

// LicenseProxy is the interface for DRM license proxying.
type LicenseProxy interface {
	// ProxyLicenseRequest proxies a license request to the DRM provider.
	ProxyLicenseRequest(ctx context.Context, system DRMSystem, request []byte) ([]byte, error)

	// GetLicenseURL returns the license URL for a DRM system.
	GetLicenseURL(ctx context.Context, system DRMSystem) (string, error)
}

// SystemIDForSystem returns the DASH-IF system ID for a DRM system.
func SystemIDForSystem(system DRMSystem) string {
	switch system {
	case SystemWidevine:
		return SystemIDWidevine
	case SystemFairPlay:
		return SystemIDFairPlay
	case SystemPlayReady:
		return SystemIDPlayReady
	case SystemClearKey:
		return SystemIDClearKey
	default:
		return ""
	}
}

// SystemFromSystemID returns the DRM system for a DASH-IF system ID.
func SystemFromSystemID(systemID string) DRMSystem {
	switch systemID {
	case SystemIDWidevine:
		return SystemWidevine
	case SystemIDFairPlay:
		return SystemFairPlay
	case SystemIDPlayReady:
		return SystemPlayReady
	case SystemIDClearKey:
		return SystemClearKey
	default:
		return ""
	}
}

// HLSKeyInfo contains information for HLS key signaling.
type HLSKeyInfo struct {
	Method           string `json:"method"`           // SAMPLE-AES, SAMPLE-AES-CTR
	URI              string `json:"uri"`              // Key URI
	KeyFormat        string `json:"keyFormat"`        // com.apple.streamingkeydelivery
	KeyFormatVersions string `json:"keyFormatVersions"` // 1
	IV               string `json:"iv,omitempty"`     // Initialization vector (hex with 0x prefix)
}

// DASHContentProtection contains information for DASH content protection.
type DASHContentProtection struct {
	SchemeIDURI   string `json:"schemeIdUri"`
	Value         string `json:"value,omitempty"`
	DefaultKID    string `json:"defaultKid,omitempty"` // UUID format with dashes
	PSSH          string `json:"pssh,omitempty"`       // Base64 encoded PSSH box
}

// PackagerDRMArgs returns DRM arguments for Shaka Packager.
func PackagerDRMArgs(config *EncryptionConfig) ([]string, error) {
	if !config.Enabled || len(config.Keys) == 0 {
		return nil, nil
	}

	key := config.Keys[0]
	args := []string{
		"--enable_raw_key_encryption",
		"--keys", fmt.Sprintf("key_id=%s:key=%s", key.KeyID, key.Key),
	}

	// Protection scheme
	scheme := string(config.Scheme)
	if scheme == "" {
		scheme = "cbcs"
	}
	args = append(args, "--protection_scheme", scheme)

	// Protection systems
	var systems []string
	for _, sys := range config.Systems {
		switch sys.System {
		case SystemWidevine:
			systems = append(systems, "Widevine")
			if sys.PSSH != "" {
				args = append(args, "--pssh", sys.PSSH)
			}
		case SystemPlayReady:
			systems = append(systems, "PlayReady")
			if sys.PSSH != "" {
				args = append(args, "--pssh", sys.PSSH)
			}
		case SystemFairPlay:
			systems = append(systems, "FairPlay")
			if sys.LicenseURL != "" {
				args = append(args, "--hls_key_uri", sys.LicenseURL)
			}
		}
	}

	if len(systems) > 0 {
		args = append(args, "--protection_systems", join(systems, ","))
	}

	// IV for FairPlay
	if key.IV != "" {
		args = append(args, "--hls_iv", key.IV)
	}

	return args, nil
}

// FFmpegDRMArgs returns DRM arguments for FFmpeg HLS encryption.
func FFmpegDRMArgs(keyInfoPath string) []string {
	return []string{"-hls_key_info_file", keyInfoPath}
}

// ValidateKeyFormat validates a hex key format (32 chars for 16 bytes).
func ValidateKeyFormat(key string) error {
	if len(key) != 32 {
		return fmt.Errorf("key must be 32 hex characters (16 bytes), got %d", len(key))
	}

	for _, c := range key {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return fmt.Errorf("key contains invalid hex character: %c", c)
		}
	}

	return nil
}

// ValidateKeyID validates a key ID format (32 hex chars or UUID format).
func ValidateKeyID(keyID string) error {
	// Remove dashes if present (UUID format)
	normalized := keyID
	for _, c := range "-" {
		normalized = removeChar(normalized, c)
	}

	return ValidateKeyFormat(normalized)
}

// NormalizeKeyID normalizes a key ID to 32 hex characters (no dashes).
func NormalizeKeyID(keyID string) string {
	normalized := keyID
	for _, c := range "-" {
		normalized = removeChar(normalized, c)
	}
	return normalized
}

// FormatKeyIDAsUUID formats a 32-char hex key ID as a UUID.
func FormatKeyIDAsUUID(keyID string) string {
	if len(keyID) != 32 {
		return keyID
	}

	return fmt.Sprintf("%s-%s-%s-%s-%s",
		keyID[0:8],
		keyID[8:12],
		keyID[12:16],
		keyID[16:20],
		keyID[20:32],
	)
}

// Helper functions

func join(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

func removeChar(s string, c rune) string {
	result := make([]rune, 0, len(s))
	for _, r := range s {
		if r != c {
			result = append(result, r)
		}
	}
	return string(result)
}
