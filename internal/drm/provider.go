package drm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel"
)

// Package-level tracer (single instance for the entire drm package).
var tracer = otel.Tracer("hls-pipeline/drm")

// KeyStore defines the interface for persistent key storage.
type KeyStore interface {
	Get(ctx context.Context, videoID string) (*ContentKey, error)
	Set(ctx context.Context, videoID string, key *ContentKey) error
}

// MultiSystemProvider implements KeyProvider for multiple DRM systems.
type MultiSystemProvider struct {
	widevine  *WidevineProvider
	fairplay  *FairPlayProvider
	playready *PlayReadyProvider

	// Persistent storage for generated keys
	keyStore KeyStore

	logger *slog.Logger
}

// ProviderConfig contains configuration for DRM providers.
type ProviderConfig struct {
	// Widevine configuration
	WidevineEnabled    bool   `json:"widevineEnabled"`
	WidevineLicenseURL string `json:"widevineLicenseUrl"`
	WidevineSignerKey  string `json:"widevineSignerKey"`
	WidevineSignerIV   string `json:"widevineSignerIv"`

	// FairPlay configuration
	FairPlayEnabled     bool   `json:"fairplayEnabled"`
	FairPlayLicenseURL  string `json:"fairplayLicenseUrl"`
	FairPlayCertificate string `json:"fairplayCertificate"`
	FairPlayASK         string `json:"fairplayAsk"` // Application Secret Key

	// PlayReady configuration
	PlayReadyEnabled    bool   `json:"playreadyEnabled"`
	PlayReadyLicenseURL string `json:"playreadyLicenseUrl"`
	PlayReadyKeyID      string `json:"playreadyKeyId"`

	// Common configuration
	DefaultScheme EncryptionScheme `json:"defaultScheme"`
	KeyStore      KeyStore         `json:"-"` // Injectable store

	Logger *slog.Logger
}

// DefaultProviderConfig returns a default configuration from environment variables.
func DefaultProviderConfig() *ProviderConfig {
	return &ProviderConfig{
		WidevineEnabled:     os.Getenv("WIDEVINE_ENABLED") == "true",
		WidevineLicenseURL:  os.Getenv("WIDEVINE_LICENSE_URL"),
		WidevineSignerKey:   os.Getenv("WIDEVINE_SIGNER_KEY"),
		WidevineSignerIV:    os.Getenv("WIDEVINE_SIGNER_IV"),
		FairPlayEnabled:     os.Getenv("FAIRPLAY_ENABLED") == "true",
		FairPlayLicenseURL:  os.Getenv("FAIRPLAY_LICENSE_URL"),
		FairPlayCertificate: os.Getenv("FAIRPLAY_CERTIFICATE"),
		FairPlayASK:         os.Getenv("FAIRPLAY_ASK"),
		PlayReadyEnabled:    os.Getenv("PLAYREADY_ENABLED") == "true",
		PlayReadyLicenseURL: os.Getenv("PLAYREADY_LICENSE_URL"),
		DefaultScheme:       EncryptionSchemeCBCS,
		Logger:              slog.Default(),
	}
}

// NewMultiSystemProvider creates a new multi-system DRM provider.
func NewMultiSystemProvider(cfg *ProviderConfig) (*MultiSystemProvider, error) {
	if cfg == nil {
		cfg = DefaultProviderConfig()
	}

	provider := &MultiSystemProvider{
		logger:   cfg.Logger,
		keyStore: cfg.KeyStore,
	}
	
	// Fallback to in-memory store if none provided (for dev/testing only)
	if provider.keyStore == nil {
		provider.keyStore = &InMemoryKeyStore{keys: make(map[string]*ContentKey)}
		provider.logger.Warn("Using in-memory key store. Not suitable for production.")
	}

	if cfg.WidevineEnabled {
		wv, err := NewWidevineProvider(&WidevineConfig{
			LicenseURL: cfg.WidevineLicenseURL,
			SignerKey:  cfg.WidevineSignerKey,
			SignerIV:   cfg.WidevineSignerIV,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create Widevine provider: %w", err)
		}
		provider.widevine = wv
	}

	if cfg.FairPlayEnabled {
		fp, err := NewFairPlayProvider(&FairPlayConfig{
			LicenseURL:  cfg.FairPlayLicenseURL,
			Certificate: cfg.FairPlayCertificate,
			ASK:         cfg.FairPlayASK,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create FairPlay provider: %w", err)
		}
		provider.fairplay = fp
	}

	if cfg.PlayReadyEnabled {
		pr, err := NewPlayReadyProvider(&PlayReadyConfig{
			LicenseURL: cfg.PlayReadyLicenseURL,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create PlayReady provider: %w", err)
		}
		provider.playready = pr
	}

	return provider, nil
}

// GetContentKey implements KeyProvider.GetContentKey.
func (p *MultiSystemProvider) GetContentKey(ctx context.Context, videoID string) (*ContentKey, error) {
	ctx, span := tracer.Start(ctx, "get-content-key")
	defer span.End()

	// Check persistent store
	cached, err := p.keyStore.Get(ctx, videoID)
	if err == nil && cached != nil {
		return cached, nil
	}

	// Generate new key
	key, err := generateContentKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate content key: %w", err)
	}

	// Save the key
	if err := p.keyStore.Set(ctx, videoID, key); err != nil {
		p.logger.ErrorContext(ctx, "Failed to save key to store", "error", err)
		// Proceed anyway, but this is risky
	}

	p.logger.InfoContext(ctx, "Generated content key",
		"videoId", videoID,
		"keyId", key.KeyID,
	)

	return key, nil
}

// GetDRMSystems implements KeyProvider.GetDRMSystems.
func (p *MultiSystemProvider) GetDRMSystems(ctx context.Context, videoID string) ([]DRMSystemConfig, error) {
	ctx, span := tracer.Start(ctx, "get-drm-systems")
	defer span.End()

	key, err := p.GetContentKey(ctx, videoID)
	if err != nil {
		return nil, err
	}

	var systems []DRMSystemConfig

	if p.widevine != nil {
		wvConfig, err := p.widevine.GetSystemConfig(ctx, key)
		if err != nil {
			p.logger.WarnContext(ctx, "Failed to get Widevine config", "error", err)
		} else {
			systems = append(systems, *wvConfig)
		}
	}

	if p.fairplay != nil {
		fpConfig, err := p.fairplay.GetSystemConfig(ctx, key)
		if err != nil {
			p.logger.WarnContext(ctx, "Failed to get FairPlay config", "error", err)
		} else {
			systems = append(systems, *fpConfig)
		}
	}

	if p.playready != nil {
		prConfig, err := p.playready.GetSystemConfig(ctx, key)
		if err != nil {
			p.logger.WarnContext(ctx, "Failed to get PlayReady config", "error", err)
		} else {
			systems = append(systems, *prConfig)
		}
	}

	return systems, nil
}

// GetEncryptionConfig implements KeyProvider.GetEncryptionConfig.
func (p *MultiSystemProvider) GetEncryptionConfig(ctx context.Context, videoID string) (*EncryptionConfig, error) {
	ctx, span := tracer.Start(ctx, "get-encryption-config")
	defer span.End()

	key, err := p.GetContentKey(ctx, videoID)
	if err != nil {
		return nil, err
	}

	systems, err := p.GetDRMSystems(ctx, videoID)
	if err != nil {
		return nil, err
	}

	// Determine encryption scheme based on enabled systems
	scheme := EncryptionSchemeCBCS
	if p.fairplay == nil && p.widevine != nil {
		// Use CENC if only Widevine (no FairPlay)
		scheme = EncryptionSchemeCENC
	}

	return &EncryptionConfig{
		Enabled: true,
		Scheme:  scheme,
		Keys:    []ContentKey{*key},
		Systems: systems,
	}, nil
}

// generateContentKey generates a new random content key.
func generateContentKey() (*ContentKey, error) {
	keyID := make([]byte, 16)
	if _, err := rand.Read(keyID); err != nil {
		return nil, fmt.Errorf("failed to generate key ID: %w", err)
	}

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("failed to generate IV: %w", err)
	}

	return &ContentKey{
		KeyID: hex.EncodeToString(keyID),
		Key:   hex.EncodeToString(key),
		IV:    hex.EncodeToString(iv),
	}, nil
}

// InMemoryKeyStore for development
type InMemoryKeyStore struct {
	keys map[string]*ContentKey
}

func (s *InMemoryKeyStore) Get(ctx context.Context, videoID string) (*ContentKey, error) {
	if key, ok := s.keys[videoID]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("key not found")
}

func (s *InMemoryKeyStore) Set(ctx context.Context, videoID string, key *ContentKey) error {
	s.keys[videoID] = key
	return nil
}
