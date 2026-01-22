package drm

import (
    "context"
    "encoding/xml"
)

// CPIX 2.3 Document structure for key exchange
type CPIXDocument struct {
    XMLName          xml.Name          `xml:"CPIX"`
    ContentKeyList   ContentKeyList    `xml:"ContentKeyList"`
    DRMSystemList    DRMSystemList     `xml:"DRMSystemList"`
    ContentKeyUsage  ContentKeyUsage   `xml:"ContentKeyUsageRuleList"`
}

type KeyProvider interface {
    // GetContentKeys retrieves encryption keys for content
    GetContentKeys(ctx context.Context, contentID string) (*CPIXDocument, error)
    
    // GetLicenseURL returns the license acquisition URL for a DRM system
    GetLicenseURL(drmSystem DRMSystem) string
}

// Example integration with a key provider (BuyDRM, PallyCon, etc.)
type ExternalKeyProvider struct {
    apiEndpoint string
    apiKey      string
    client      *http.Client
}

func (p *ExternalKeyProvider) GetContentKeys(ctx context.Context, contentID string) (*CPIXDocument, error) {
    req, _ := http.NewRequestWithContext(ctx, "POST", 
        fmt.Sprintf("%s/cpix/v2/contentkeys", p.apiEndpoint),
        strings.NewReader(fmt.Sprintf(`{"content_id": "%s"}`, contentID)))
    
    req.Header.Set("X-API-Key", p.apiKey)
    req.Header.Set("Content-Type", "application/json")
    
    resp, err := p.client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var cpix CPIXDocument
    if err := xml.NewDecoder(resp.Body).Decode(&cpix); err != nil {
        return nil, err
    }
    
    return &cpix, nil
}
