package drm

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// PlayReadyConfig contains PlayReady-specific configuration.
type PlayReadyConfig struct {
	LicenseURL string `json:"licenseUrl"`
	CustomData string `json:"customData,omitempty"`
}

// PlayReadyHeader contains the PlayReady header information.
type PlayReadyHeader struct {
	KeyID      string `json:"keyId"`
	LicenseURL string `json:"licenseUrl"`
	CustomData string `json:"customData,omitempty"`
	CheckSum   string `json:"checkSum,omitempty"`
}

// GeneratePlayReadyPSSH generates a PlayReady PSSH box.
func GeneratePlayReadyPSSH(keyID string, licenseURL string) (string, error) {
	// Generate PlayReady header
	header, err := GeneratePlayReadyHeader(keyID, licenseURL, "")
	if err != nil {
		return "", err
	}

	// Convert header to UTF-16LE bytes
	headerBytes := stringToUTF16LE(header)

	// Build PlayReady Object
	proData := buildPlayReadyObject(headerBytes)

	// Build PSSH box
	psshBox := buildPSSHBox(SystemIDPlayReady, proData)

	return base64.StdEncoding.EncodeToString(psshBox), nil
}

// GeneratePlayReadyHeader generates the PlayReady header XML.
func GeneratePlayReadyHeader(keyID string, licenseURL string, customData string) (string, error) {
	// Convert key ID format if needed
	formattedKID := formatPlayReadyKeyID(keyID)

	var header strings.Builder
	header.WriteString(`<WRMHEADER xmlns="http://schemas.microsoft.com/DRM/2007/03/PlayReadyHeader" version="4.0.0.0">`)
	header.WriteString(`<DATA>`)
	header.WriteString(`<PROTECTINFO>`)
	header.WriteString(`<KEYLEN>16</KEYLEN>`)
	header.WriteString(`<ALGID>AESCTR</ALGID>`)
	header.WriteString(`</PROTECTINFO>`)
	header.WriteString(`<KID>` + formattedKID + `</KID>`)
	
	if licenseURL != "" {
		header.WriteString(`<LA_URL>` + escapeXML(licenseURL) + `</LA_URL>`)
	}
	
	if customData != "" {
		header.WriteString(`<CUSTOMATTRIBUTES>`)
		header.WriteString(`<IIS_DRM_VERSION>8.0</IIS_DRM_VERSION>`)
		header.WriteString(customData)
		header.WriteString(`</CUSTOMATTRIBUTES>`)
	}
	
	header.WriteString(`</DATA>`)
	header.WriteString(`</WRMHEADER>`)

	return header.String(), nil
}

// formatPlayReadyKeyID formats a key ID for PlayReady.
// PlayReady uses a specific byte-swapped format for the key ID.
func formatPlayReadyKeyID(keyID string) string {
	// Remove dashes if present
	keyID = strings.ReplaceAll(keyID, "-", "")
	
	if len(keyID) != 32 {
		return keyID
	}

	// PlayReady expects the key ID in a specific format:
	// The first three components are byte-swapped (little-endian)
	// xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	// becomes: little(xxxxxxxx)-little(xxxx)-little(xxxx)-xxxx-xxxxxxxxxxxx
	
	// Parse as standard UUID format
	part1 := keyID[0:8]   // First 4 bytes
	part2 := keyID[8:12]  // Next 2 bytes
	part3 := keyID[12:16] // Next 2 bytes
	part4 := keyID[16:20] // Next 2 bytes (not swapped)
	part5 := keyID[20:32] // Last 6 bytes (not swapped)

	// Byte-swap the first three parts
	swapped1 := swapBytes(part1)
	swapped2 := swapBytes(part2)
	swapped3 := swapBytes(part3)

	swappedKID := swapped1 + swapped2 + swapped3 + part4 + part5

	// Base64 encode the key ID bytes
	kidBytes := make([]byte, 16)
	for i := 0; i < 16; i++ {
		fmt.Sscanf(swappedKID[i*2:i*2+2], "%02x", &kidBytes[i])
	}

	return base64.StdEncoding.EncodeToString(kidBytes)
}

// swapBytes swaps bytes in a hex string.
func swapBytes(hex string) string {
	if len(hex)%2 != 0 {
		return hex
	}

	result := make([]byte, len(hex))
	for i := 0; i < len(hex); i += 2 {
		result[len(hex)-2-i] = hex[i]
		result[len(hex)-1-i] = hex[i+1]
	}

	return string(result)
}

// buildPlayReadyObject builds the PlayReady Object structure.
func buildPlayReadyObject(headerBytes []byte) []byte {
	// PlayReady Object structure:
	// - 4 bytes: length (little-endian)
	// - 2 bytes: record count (little-endian)
	// - Records:
	//   - 2 bytes: record type (little-endian)
	//   - 2 bytes: record length (little-endian)
	//   - N bytes: record value

	recordType := uint16(1) // Rights Management Header
	recordLength := uint16(len(headerBytes))

	// Total length: 4 (length) + 2 (count) + 2 (type) + 2 (rec length) + header
	totalLength := uint32(4 + 2 + 2 + 2 + len(headerBytes))

	data := make([]byte, totalLength)
	offset := 0

	// Length (little-endian)
	binary.LittleEndian.PutUint32(data[offset:], totalLength)
	offset += 4

	// Record count (little-endian)
	binary.LittleEndian.PutUint16(data[offset:], 1)
	offset += 2

	// Record type (little-endian)
	binary.LittleEndian.PutUint16(data[offset:], recordType)
	offset += 2

	// Record length (little-endian)
	binary.LittleEndian.PutUint16(data[offset:], recordLength)
	offset += 2

	// Header
	copy(data[offset:], headerBytes)

	return data
}

// stringToUTF16LE converts a string to UTF-16LE bytes.
func stringToUTF16LE(s string) []byte {
	encoded := utf16.Encode([]rune(s))
	result := make([]byte, len(encoded)*2)
	
	for i, r := range encoded {
		binary.LittleEndian.PutUint16(result[i*2:], r)
	}
	
	return result
}

// escapeXML escapes special XML characters.
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// PlayReadyContentProtection generates the DASH ContentProtection element for PlayReady.
func PlayReadyContentProtection(keyID string, pssh string, licenseURL string) string {
	var builder strings.Builder
	
	builder.WriteString(fmt.Sprintf(`<ContentProtection schemeIdUri="urn:uuid:%s">`, SystemIDPlayReady))
	builder.WriteString("\n")
	builder.WriteString(fmt.Sprintf(`  <cenc:pssh>%s</cenc:pssh>`, pssh))
	builder.WriteString("\n")
	
	if licenseURL != "" {
		builder.WriteString(fmt.Sprintf(`  <mspr:pro xmlns:mspr="urn:microsoft:playready">%s</mspr:pro>`, pssh))
		builder.WriteString("\n")
	}
	
	builder.WriteString(`</ContentProtection>`)
	
	return builder.String()
}

// GetPlayReadyPackagerArgs returns Shaka Packager arguments for PlayReady encryption.
func GetPlayReadyPackagerArgs(keyID, key, licenseURL string) []string {
	args := []string{
		"--protection_scheme", "cenc", // PlayReady typically uses CENC
		"--protection_systems", "PlayReady",
	}

	if licenseURL != "" {
		// Generate and include PSSH
		pssh, err := GeneratePlayReadyPSSH(keyID, licenseURL)
		if err == nil {
			args = append(args, "--pssh", pssh)
		}
	}

	return args
}

// ValidatePlayReadyConfig validates PlayReady configuration.
func ValidatePlayReadyConfig(cfg *PlayReadyConfig) error {
	if cfg.LicenseURL == "" {
		return fmt.Errorf("licenseUrl is required for PlayReady")
	}
	return nil
}

