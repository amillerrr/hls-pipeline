package drm

type FairPlayConfig struct {
    CertificatePath string
    ASK             string // Application Secret Key
    KeyServerModule string
}

// GenerateFairPlayKeyInfo creates the key info for HLS playlists
func GenerateFairPlayKeyInfo(keyID string, keyURI string) string {
    // FairPlay uses skd:// URI scheme
    return fmt.Sprintf(`#EXT-X-KEY:METHOD=SAMPLE-AES,URI="skd://%s",KEYFORMAT="com.apple.streamingkeydelivery",KEYFORMATVERSIONS="1"`, keyURI)
}
