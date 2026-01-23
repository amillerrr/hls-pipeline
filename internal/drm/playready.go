package drm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf16"
)

// PlayReadyConfig contains PlayReady-specific configuration.
type PlayReadyConfig struct {
	LicenseURL string `json:"licenseUrl"`
	KeySeed    string `json:"keySeed"` // Base64 encoded key seed (optional)
}

// PlayReadyProvider implements PlayReady DRM key provisioning.
type PlayReadyProvider struct {
	config  *PlayReadyConfig
	client  *http.Client
	keySeed []byte
}

// NewPlayReadyProvider creates a new PlayReady provider.
func NewPlayReadyProvider(cfg *PlayReadyConfig) (*PlayReadyProvider, error) {
	if cfg.LicenseURL == "" {
		return nil, fmt.Errorf("PlayReady license URL is required")
	}

	provider := &PlayReadyProvider{
		config: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Decode key seed if provided
	if cfg.KeySeed != "" {
		seed, err := base64.StdEncoding.DecodeString(cfg.KeySeed)
		if err != nil {
			return nil, fmt.Errorf("invalid PlayReady key seed: %w", err)
		}
		provider.keySeed = seed
	}

	return provider, nil
}

// GetSystemConfig returns the PlayReady DRM system configuration.
func (p *PlayReadyProvider) GetSystemConfig(ctx context.Context, key *ContentKey) (*DRMSystemConfig, error) {
	_, span := tracer.Start(ctx, "playready-get-system-config")
	defer span.End()

	// Generate PSSH box for PlayReady
	pssh, err := p.generatePSSH(key)
	if err != nil {
		return nil, fmt.Errorf("failed to generate PlayReady PSSH: %w", err)
	}

	return &DRMSystemConfig{
		System:     SystemPlayReady,
		SystemID:   SystemIDPlayReady,
		LicenseURL: p.config.LicenseURL,
		PSSH:       pssh,
	}, nil
}

// generatePSSH generates a PlayReady PSSH box with PRO (PlayReady Object).
func (p *PlayReadyProvider) generatePSSH(key *ContentKey) (string, error) {
	// Generate PlayReady Header (PRH)
	prh, err := p.generatePlayReadyHeader(key)
	if err != nil {
		return "", fmt.Errorf("failed to generate PRH: %w", err)
	}

	// Generate PlayReady Object (PRO)
	pro := p.generatePlayReadyObject(prh)

	// Generate PSSH box
	systemID, _ := hexDecode(NormalizeKeyID(SystemIDPlayReady))

	var pssh bytes.Buffer

	// Box size placeholder
	boxSize := 12 + 16 + 4 + len(pro)

	// Box size
	writeUint32BE(&pssh, uint32(boxSize))
	// Box type
	pssh.WriteString("pssh")
	// Version and flags
	pssh.WriteByte(0) // Version 0
	pssh.Write([]byte{0, 0, 0}) // Flags
	// System ID
	pssh.Write(systemID)
	// Data size
	writeUint32BE(&pssh, uint32(len(pro)))
	// PRO data
	pssh.Write(pro)

	return base64.StdEncoding.EncodeToString(pssh.Bytes()), nil
}

// PlayReadyHeader represents the PlayReady Header XML structure.
type PlayReadyHeader struct {
	XMLName xml.Name `xml:"WRMHEADER"`
	Version string   `xml:"version,attr"`
	Data    struct {
		ProtectInfo struct {
			Kids struct {
				Kid string `xml:"KID"`
			} `xml:"KIDS"`
		} `xml:"PROTECTINFO"`
		LA_URL        string `xml:"LA_URL,omitempty"`
		LUI_URL       string `xml:"LUI_URL,omitempty"`
		DS_ID         string `xml:"DS_ID,omitempty"`
		CustomAttribs string `xml:"CUSTOMATTRIBUTES>any,omitempty"`
	} `xml:"DATA"`
}

// generatePlayReadyHeader generates the PlayReady Header XML.
func (p *PlayReadyProvider) generatePlayReadyHeader(key *ContentKey) ([]byte, error) {
	// Convert key ID to PlayReady format (base64 of GUID in little-endian)
	keyIDBytes, err := hexDecode(key.KeyID)
	if err != nil {
		return nil, fmt.Errorf("invalid key ID: %w", err)
	}

	// Swap bytes for little-endian GUID format
	swappedKeyID := swapGUIDBytes(keyIDBytes)
	kidBase64 := base64.StdEncoding.EncodeToString(swappedKeyID)

	// Create XML header
	xmlStr := fmt.Sprintf(`<WRMHEADER xmlns="http://schemas.microsoft.com/DRM/2007/03/PlayReadyHeader" version="4.0.0.0"><DATA><PROTECTINFO><KEYLEN>16</KEYLEN><ALGID>AESCTR</ALGID></PROTECTINFO><KID>%s</KID><LA_URL>%s</LA_URL><CHECKSUM></CHECKSUM></DATA></WRMHEADER>`,
		kidBase64,
		p.config.LicenseURL,
	)

	return []byte(xmlStr), nil
}

// generatePlayReadyObject wraps the PRH in a PlayReady Object structure.
func (p *PlayReadyProvider) generatePlayReadyObject(prh []byte) []byte {
	// Convert PRH to UTF-16LE
	utf16LE := stringToUTF16LE(string(prh))

	// Record size (including header)
	recordSize := len(utf16LE) + 4 // 2 bytes type + 2 bytes length
	proSize := recordSize + 6      // 4 bytes total size + 2 bytes record count

	var pro bytes.Buffer

	// Total PRO size (little-endian)
	writeUint32LE(&pro, uint32(proSize))
	// Record count
	writeUint16LE(&pro, 1)
	// Record type (1 = Rights Management Header)
	writeUint16LE(&pro, 1)
	// Record length
	writeUint16LE(&pro, uint16(len(utf16LE)))
	// Record value (UTF-16LE PRH)
	pro.Write(utf16LE)

	return pro.Bytes()
}

// GetLicense makes a license request to the PlayReady license server.
func (p *PlayReadyProvider) GetLicense(ctx context.Context, challenge []byte) ([]byte, error) {
	ctx, span := tracer.Start(ctx, "playready-get-license")
	defer span.End()

	req, err := http.NewRequestWithContext(ctx, "POST", p.config.LicenseURL, bytes.NewReader(challenge))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "text/xml; charset=utf-8")

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

// Helper functions

func swapGUIDBytes(guid []byte) []byte {
	if len(guid) != 16 {
		return guid
	}

	swapped := make([]byte, 16)
	// Swap first 4 bytes
	swapped[0] = guid[3]
	swapped[1] = guid[2]
	swapped[2] = guid[1]
	swapped[3] = guid[0]
	// Swap next 2 bytes
	swapped[4] = guid[5]
	swapped[5] = guid[4]
	// Swap next 2 bytes
	swapped[6] = guid[7]
	swapped[7] = guid[6]
	// Keep remaining 8 bytes as-is
	copy(swapped[8:], guid[8:])

	return swapped
}

func stringToUTF16LE(s string) []byte {
	runes := []rune(s)
	utf16Codes := utf16.Encode(runes)

	result := make([]byte, len(utf16Codes)*2)
	for i, code := range utf16Codes {
		result[i*2] = byte(code)
		result[i*2+1] = byte(code >> 8)
	}

	return result
}

func writeUint32LE(buf *bytes.Buffer, v uint32) {
	buf.WriteByte(byte(v))
	buf.WriteByte(byte(v >> 8))
	buf.WriteByte(byte(v >> 16))
	buf.WriteByte(byte(v >> 24))
}

func writeUint16LE(buf *bytes.Buffer, v uint16) {
	buf.WriteByte(byte(v))
	buf.WriteByte(byte(v >> 8))
}

