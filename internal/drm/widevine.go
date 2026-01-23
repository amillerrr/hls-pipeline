package drm

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
)

// WidevinePSSHVersion is the PSSH box version for Widevine.
const WidevinePSSHVersion = 0

// WidevineConfig contains Widevine-specific configuration.
type WidevineConfig struct {
	LicenseURL string `json:"licenseUrl"`
	Provider   string `json:"provider,omitempty"`
	ContentID  string `json:"contentId,omitempty"`
	Policy     string `json:"policy,omitempty"`
}

// WidevinePSSHData contains the data for a Widevine PSSH box.
type WidevinePSSHData struct {
	Algorithm       int      `json:"algorithm,omitempty"`       // 1 = AES-CTR, 2 = AES-CBC
	KeyIDs          [][]byte `json:"keyIds,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	ContentID       []byte   `json:"contentId,omitempty"`
	Policy          string   `json:"policy,omitempty"`
	CryptoPeriodIndex uint32 `json:"cryptoPeriodIndex,omitempty"`
}

// GenerateWidevinePSSH generates a Widevine PSSH box.
func GenerateWidevinePSSH(keyID []byte, contentID string, provider string) (string, error) {
	if len(keyID) != 16 {
		return "", fmt.Errorf("key ID must be 16 bytes")
	}

	// Build Widevine-specific data
	// This is a simplified implementation - full implementation would use protobuf
	psshData := buildWidevinePSSHData(keyID, contentID, provider)

	// Build full PSSH box
	psshBox := buildPSSHBox(SystemIDWidevine, psshData)

	return base64.StdEncoding.EncodeToString(psshBox), nil
}

// buildWidevinePSSHData builds the Widevine-specific PSSH data.
func buildWidevinePSSHData(keyID []byte, contentID string, provider string) []byte {
	// Simplified Widevine PSSH data structure
	// In production, use google.golang.org/protobuf with Widevine's proto definition
	
	var data []byte

	// Algorithm (field 1, varint) - AESCTR = 1
	data = append(data, 0x08, 0x01)

	// Key ID (field 2, bytes)
	data = append(data, 0x12, byte(len(keyID)))
	data = append(data, keyID...)

	// Provider (field 3, string) - optional
	if provider != "" {
		data = append(data, 0x1a, byte(len(provider)))
		data = append(data, []byte(provider)...)
	}

	// Content ID (field 4, bytes) - optional
	if contentID != "" {
		contentBytes := []byte(contentID)
		data = append(data, 0x22, byte(len(contentBytes)))
		data = append(data, contentBytes...)
	}

	return data
}

// buildPSSHBox builds a PSSH box with the given system ID and data.
func buildPSSHBox(systemID string, data []byte) []byte {
	// Parse system ID (UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)
	systemIDBytes := parseUUID(systemID)

	// PSSH box structure:
	// - 4 bytes: box size
	// - 4 bytes: box type ('pssh')
	// - 1 byte: version
	// - 3 bytes: flags
	// - 16 bytes: system ID
	// - 4 bytes: data size (if version 1, also KID count and KIDs before this)
	// - N bytes: data

	boxSize := 4 + 4 + 1 + 3 + 16 + 4 + len(data)
	
	box := make([]byte, boxSize)
	offset := 0

	// Box size (big-endian)
	binary.BigEndian.PutUint32(box[offset:], uint32(boxSize))
	offset += 4

	// Box type
	copy(box[offset:], []byte("pssh"))
	offset += 4

	// Version and flags
	box[offset] = WidevinePSSHVersion
	offset += 4 // version (1) + flags (3)

	// System ID
	copy(box[offset:], systemIDBytes)
	offset += 16

	// Data size
	binary.BigEndian.PutUint32(box[offset:], uint32(len(data)))
	offset += 4

	// Data
	copy(box[offset:], data)

	return box
}

// parseUUID parses a UUID string into bytes.
func parseUUID(uuid string) []byte {
	result := make([]byte, 16)
	idx := 0
	
	for i := 0; i < len(uuid) && idx < 16; i++ {
		c := uuid[i]
		if c == '-' {
			continue
		}

		var val byte
		if c >= '0' && c <= '9' {
			val = c - '0'
		} else if c >= 'a' && c <= 'f' {
			val = c - 'a' + 10
		} else if c >= 'A' && c <= 'F' {
			val = c - 'A' + 10
		}

		if i%2 == 0 || (i > 0 && uuid[i-1] == '-') {
			result[idx] = val << 4
		} else {
			result[idx] |= val
			idx++
		}
	}

	return result
}

// GetWidevineLicenseURL returns the license URL for Widevine.
func GetWidevineLicenseURL(cfg *WidevineConfig) string {
	if cfg.LicenseURL != "" {
		return cfg.LicenseURL
	}
	// Default Widevine proxy URL (should be configured)
	return ""
}

// WidevineContentProtection generates the DASH ContentProtection element for Widevine.
func WidevineContentProtection(keyID string, pssh string) string {
	return fmt.Sprintf(`<ContentProtection schemeIdUri="urn:uuid:%s" value="Widevine">
  <cenc:pssh>%s</cenc:pssh>
</ContentProtection>`, SystemIDWidevine, pssh)
}

// WidevineCencHeader generates the Widevine CENC header for DASH.
func WidevineCencHeader(keyID string) string {
	return fmt.Sprintf(`<ContentProtection schemeIdUri="urn:mpeg:dash:mp4protection:2011" value="cenc" cenc:default_KID="%s"/>`, keyID)
}
