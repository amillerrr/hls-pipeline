package drm

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CPIX 2.3 Document structure for key exchange

// CPIXDocument represents a CPIX 2.3 document for key exchange.
type CPIXDocument struct {
	XMLName         xml.Name         `xml:"CPIX"`
	Xmlns           string           `xml:"xmlns,attr"`
	XmlnsPSSH       string           `xml:"xmlns:pskc,attr,omitempty"`
	XmlnsEnc        string           `xml:"xmlns:enc,attr,omitempty"`
	ID              string           `xml:"id,attr,omitempty"`
	ContentKeyList  *ContentKeyList  `xml:"ContentKeyList,omitempty"`
	DRMSystemList   *DRMSystemList   `xml:"DRMSystemList,omitempty"`
	ContentKeyUsage *ContentKeyUsage `xml:"ContentKeyUsageRuleList,omitempty"`
}

// ContentKeyList contains the content encryption keys.
type ContentKeyList struct {
	ContentKeys []CPIXContentKey `xml:"ContentKey"`
}

// CPIXContentKey represents a content key in CPIX format.
type CPIXContentKey struct {
	KID         string       `xml:"kid,attr"`
	CommonKeyID string       `xml:"commonEncryptionScheme,attr,omitempty"`
	Data        *KeyData     `xml:"Data,omitempty"`
	ExplicitIV  string       `xml:"explicitIV,attr,omitempty"`
}

// KeyData contains the encrypted key material.
type KeyData struct {
	Secret *Secret `xml:"pskc:Secret,omitempty"`
}

// Secret contains the actual key value.
type Secret struct {
	PlainValue   string `xml:"pskc:PlainValue,omitempty"`
	EncryptedValue string `xml:"pskc:EncryptedValue,omitempty"`
}

// DRMSystemList contains DRM system configurations.
type DRMSystemList struct {
	DRMSystems []CPIXDRMSystem `xml:"DRMSystem"`
}

// CPIXDRMSystem represents a DRM system configuration in CPIX.
type CPIXDRMSystem struct {
	KID        string `xml:"kid,attr"`
	SystemID   string `xml:"systemId,attr"`
	PSSH       string `xml:"PSSH,omitempty"`
	ContentProtectionData string `xml:"ContentProtectionData,omitempty"`
	HLSSignalingData      string `xml:"HLSSignalingData,omitempty"`
	URIExtXKey string `xml:"URIExtXKey,omitempty"`
}

// ContentKeyUsage defines usage rules for content keys.
type ContentKeyUsage struct {
	Rules []ContentKeyUsageRule `xml:"ContentKeyUsageRule"`
}

// ContentKeyUsageRule defines a single usage rule.
type ContentKeyUsageRule struct {
	KID          string        `xml:"kid,attr"`
	IntendedTrackType string   `xml:"intendedTrackType,attr,omitempty"`
	AudioFilter  *TrackFilter  `xml:"AudioFilter,omitempty"`
	VideoFilter  *TrackFilter  `xml:"VideoFilter,omitempty"`
}

// TrackFilter defines filtering criteria for track selection.
type TrackFilter struct {
	MinPixels int `xml:"minPixels,attr,omitempty"`
	MaxPixels int `xml:"maxPixels,attr,omitempty"`
	HDR       string `xml:"hdr,attr,omitempty"`
}

// KeyServerProvider defines the interface for external key providers.
type KeyServerProvider interface {
	// GetContentKeys retrieves encryption keys for content.
	GetContentKeys(ctx context.Context, contentID string) (*CPIXDocument, error)

	// GetLicenseURL returns the license acquisition URL for a DRM system.
	GetLicenseURL(drmSystem DRMSystem) string

	// RotateKey requests a new key for the content.
	RotateKey(ctx context.Context, contentID string) (*CPIXDocument, error)
}

// ExternalKeyProvider implements KeyServerProvider for external key servers.
type ExternalKeyProvider struct {
	apiEndpoint string
	apiKey      string
	client      *http.Client
	licenseURLs map[DRMSystem]string
}

// ExternalKeyProviderConfig contains configuration for external key providers.
type ExternalKeyProviderConfig struct {
	APIEndpoint         string
	APIKey              string
	Timeout             time.Duration
	WidevineLicenseURL  string
	FairPlayLicenseURL  string
	PlayReadyLicenseURL string
}

// NewExternalKeyProvider creates a new external key provider.
func NewExternalKeyProvider(cfg *ExternalKeyProviderConfig) (*ExternalKeyProvider, error) {
	if cfg.APIEndpoint == "" {
		return nil, fmt.Errorf("API endpoint is required")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &ExternalKeyProvider{
		apiEndpoint: cfg.APIEndpoint,
		apiKey:      cfg.APIKey,
		client: &http.Client{
			Timeout: timeout,
		},
		licenseURLs: map[DRMSystem]string{
			SystemWidevine:  cfg.WidevineLicenseURL,
			SystemFairPlay:  cfg.FairPlayLicenseURL,
			SystemPlayReady: cfg.PlayReadyLicenseURL,
		},
	}, nil
}

// GetContentKeys retrieves encryption keys from the external key server.
func (p *ExternalKeyProvider) GetContentKeys(ctx context.Context, contentID string) (*CPIXDocument, error) {
	url := fmt.Sprintf("%s/cpix/v2/contentkeys", p.apiEndpoint)

	body := strings.NewReader(fmt.Sprintf(`{"content_id": "%s"}`, contentID))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/xml")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get content keys: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("key server returned status %d", resp.StatusCode)
	}

	var cpix CPIXDocument
	if err := xml.NewDecoder(resp.Body).Decode(&cpix); err != nil {
		return nil, fmt.Errorf("failed to decode CPIX response: %w", err)
	}

	return &cpix, nil
}

// GetLicenseURL returns the license URL for a DRM system.
func (p *ExternalKeyProvider) GetLicenseURL(drmSystem DRMSystem) string {
	return p.licenseURLs[drmSystem]
}

// RotateKey requests a new key from the key server.
func (p *ExternalKeyProvider) RotateKey(ctx context.Context, contentID string) (*CPIXDocument, error) {
	url := fmt.Sprintf("%s/cpix/v2/contentkeys/%s/rotate", p.apiEndpoint, contentID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("Accept", "application/xml")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to rotate key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("key rotation returned status %d", resp.StatusCode)
	}

	var cpix CPIXDocument
	if err := xml.NewDecoder(resp.Body).Decode(&cpix); err != nil {
		return nil, fmt.Errorf("failed to decode CPIX response: %w", err)
	}

	return &cpix, nil
}

// CPIXToEncryptionConfig converts a CPIX document to our internal EncryptionConfig.
func CPIXToEncryptionConfig(cpix *CPIXDocument) (*EncryptionConfig, error) {
	if cpix == nil || cpix.ContentKeyList == nil || len(cpix.ContentKeyList.ContentKeys) == 0 {
		return nil, fmt.Errorf("CPIX document contains no content keys")
	}

	config := &EncryptionConfig{
		Enabled: true,
		Scheme:  EncryptionSchemeCBCS, // Default to CBCS
		Keys:    make([]ContentKey, 0, len(cpix.ContentKeyList.ContentKeys)),
		Systems: make([]DRMSystemConfig, 0),
	}

	// Extract content keys
	for _, ck := range cpix.ContentKeyList.ContentKeys {
		key := ContentKey{
			KeyID: ck.KID,
			IV:    ck.ExplicitIV,
		}

		if ck.Data != nil && ck.Data.Secret != nil {
			key.Key = ck.Data.Secret.PlainValue
		}

		config.Keys = append(config.Keys, key)
	}

	// Extract DRM system configurations
	if cpix.DRMSystemList != nil {
		for _, ds := range cpix.DRMSystemList.DRMSystems {
			system := SystemFromSystemID(ds.SystemID)
			if system == "" {
				continue
			}

			sysConfig := DRMSystemConfig{
				System:   system,
				SystemID: ds.SystemID,
				PSSH:     ds.PSSH,
			}

			if ds.URIExtXKey != "" {
				sysConfig.LicenseURL = ds.URIExtXKey
			}

			config.Systems = append(config.Systems, sysConfig)
		}
	}

	return config, nil
}

// BuyDRMProvider implements KeyServerProvider for BuyDRM KeyOS.
type BuyDRMProvider struct {
	*ExternalKeyProvider
}

// NewBuyDRMProvider creates a new BuyDRM provider.
func NewBuyDRMProvider(apiKey, userID string) (*BuyDRMProvider, error) {
	provider, err := NewExternalKeyProvider(&ExternalKeyProviderConfig{
		APIEndpoint:         "https://api.buydrm.com",
		APIKey:              apiKey,
		WidevineLicenseURL:  "https://wv.buydrm.com/modular/license",
		FairPlayLicenseURL:  "https://fp.buydrm.com/fps/getkey",
		PlayReadyLicenseURL: "https://pr.buydrm.com/playready/license",
	})
	if err != nil {
		return nil, err
	}

	return &BuyDRMProvider{ExternalKeyProvider: provider}, nil
}

// PallyConProvider implements KeyServerProvider for PallyCon.
type PallyConProvider struct {
	*ExternalKeyProvider
}

// NewPallyConProvider creates a new PallyCon provider.
func NewPallyConProvider(siteID, siteKey string) (*PallyConProvider, error) {
	provider, err := NewExternalKeyProvider(&ExternalKeyProviderConfig{
		APIEndpoint:         fmt.Sprintf("https://kms.pallycon.com/v2/cpix/pallycon/getContentKey/%s", siteID),
		APIKey:              siteKey,
		WidevineLicenseURL:  fmt.Sprintf("https://license.pallycon.com/ri/widevine/%s", siteID),
		FairPlayLicenseURL:  fmt.Sprintf("https://license.pallycon.com/ri/fairplay/%s", siteID),
		PlayReadyLicenseURL: fmt.Sprintf("https://license.pallycon.com/ri/playready/%s", siteID),
	})
	if err != nil {
		return nil, err
	}

	return &PallyConProvider{ExternalKeyProvider: provider}, nil
}

// AxinomProvider implements KeyServerProvider for Axinom DRM.
type AxinomProvider struct {
	*ExternalKeyProvider
}

// NewAxinomProvider creates a new Axinom provider.
func NewAxinomProvider(tenantID, apiKey string) (*AxinomProvider, error) {
	provider, err := NewExternalKeyProvider(&ExternalKeyProviderConfig{
		APIEndpoint:         fmt.Sprintf("https://key-server.axprod.net/api/CPIX/%s", tenantID),
		APIKey:              apiKey,
		WidevineLicenseURL:  fmt.Sprintf("https://drm-widevine-licensing.axprod.net/AcquireLicense/%s", tenantID),
		FairPlayLicenseURL:  fmt.Sprintf("https://drm-fairplay-licensing.axprod.net/AcquireLicense/%s", tenantID),
		PlayReadyLicenseURL: fmt.Sprintf("https://drm-playready-licensing.axprod.net/AcquireLicense/%s", tenantID),
	})
	if err != nil {
		return nil, err
	}

	return &AxinomProvider{ExternalKeyProvider: provider}, nil
}
