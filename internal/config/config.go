package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration.
type Config struct {
	Environment   string
	AWS           AWSConfig
	API           APIConfig
	Worker        WorkerConfig
	Transcoding   TranscodingConfig
	CDN           CDNConfig
	DRM           DRMConfig
	SSAI          SSAIConfig
	Observability ObservabilityConfig
	CORS          CORSConfig
}

// AWSConfig holds AWS-specific configuration.
type AWSConfig struct {
	Region          string
	RawBucket       string
	ProcessedBucket string
	SQSQueueURL     string
	DynamoDBTable   string
	CDNDomain       string
	AdsBucket       string
	LogsBucket      string
}

// APIConfig holds API server configuration.
type APIConfig struct {
	Port          string
	Username      string
	Password      string
	JWTSecret     string
	MaxUploadSize int64
	PresignExpiry time.Duration
}

// WorkerConfig holds worker-specific configuration.
type WorkerConfig struct {
	MaxConcurrentJobs int
	MetricsPort       int
	TempDir           string
	JobTimeout        time.Duration
}

// TranscodingConfig holds transcoding configuration.
type TranscodingConfig struct {
	EnableLLHLS     bool
	EnableCMAF      bool
	SegmentDuration float64
	PartDuration    float64
	UseShaka        bool
	PresetsFile     string
	Presets         []PresetConfig
}

// CDNConfig holds CDN configuration.
type CDNConfig struct {
	EnableMultiCDN    bool
	PrimaryProvider   string
	Providers         []CDNProviderConfig
	HealthCheckPeriod time.Duration
	SessionTTL        time.Duration
	Strategy          string
}

// CDNProviderConfig holds configuration for a single CDN provider.
type CDNProviderConfig struct {
	Name       string `json:"name" yaml:"name"`
	BaseURL    string `json:"baseUrl" yaml:"baseUrl"`
	Weight     int    `json:"weight" yaml:"weight"`
	Priority   int    `json:"priority" yaml:"priority"`
	HealthURL  string `json:"healthUrl" yaml:"healthUrl"`
	SigningKey string `json:"signingKey" yaml:"signingKey"`
	Region     string `json:"region" yaml:"region"`
}

// DRMConfig holds DRM configuration.
type DRMConfig struct {
	Enabled             bool
	Provider            string
	KeyServerURL        string
	APIKey              string
	WidevineLicenseURL  string
	FairPlayLicenseURL  string
	PlayReadyLicenseURL string
	FairPlayCertURL     string
	EncryptionScheme    string
	KeyRotationPeriod   time.Duration
}

// SSAIConfig holds server-side ad insertion configuration.
type SSAIConfig struct {
	Enabled                  bool
	AdDecisionServerURL      string
	PrerollAdServerURL       string
	SlateAdURL               string
	PersonalizationThreshold int
	MaxAdDuration            int
	MediaTailorConfigName    string
	EnableSCTE35Passthrough  bool
	PreserveSCTE35Markers    bool
}

// ObservabilityConfig holds observability configuration.
type ObservabilityConfig struct {
	OTLPEndpoint    string
	EnableTracing   bool
	EnableMetrics   bool
	ServiceName     string
	ServiceVersion  string
	TraceSampleRate float64
}

// CORSConfig holds CORS configuration.
type CORSConfig struct {
	AllowedOrigins []string
	AllowedMethods []string
	AllowedHeaders []string
	MaxAge         int
}

// Default values
const (
	DefaultPort              = "8080"
	DefaultMetricsPort       = 2112
	DefaultMaxConcurrentJobs = 2
	DefaultOTLPEndpoint      = "localhost:4317"
	DefaultRegion            = "us-west-2"
	DefaultSegmentDuration   = 4.0
	DefaultPartDuration      = 0.5
	DefaultMaxUploadSize     = 10 * 1024 * 1024 * 1024
	DefaultPresignExpiry     = 15 * time.Minute
	DefaultJobTimeout        = 30 * time.Minute
	DefaultHealthCheckPeriod = 30 * time.Second
	DefaultSessionTTL        = 2 * time.Hour
)

// Load reads configuration from environment variables and returns a validated Config.
func Load() (*Config, error) {
	cfg := &Config{
		Environment: getEnv("ENV", "dev"),
		AWS: AWSConfig{
			Region:          getEnv("AWS_REGION", DefaultRegion),
			RawBucket:       os.Getenv("S3_BUCKET"),
			ProcessedBucket: os.Getenv("PROCESSED_BUCKET"),
			SQSQueueURL:     os.Getenv("SQS_QUEUE_URL"),
			DynamoDBTable:   os.Getenv("DYNAMODB_TABLE"),
			CDNDomain:       os.Getenv("CDN_DOMAIN"),
			AdsBucket:       os.Getenv("ADS_BUCKET"),
			LogsBucket:      os.Getenv("LOGS_BUCKET"),
		},
		API: APIConfig{
			Port:          getEnv("PORT", DefaultPort),
			Username:      os.Getenv("API_USERNAME"),
			Password:      os.Getenv("API_PASSWORD"),
			JWTSecret:     os.Getenv("JWT_SECRET"),
			MaxUploadSize: getEnvInt64("MAX_UPLOAD_SIZE", DefaultMaxUploadSize),
			PresignExpiry: getEnvDuration("PRESIGN_EXPIRY", DefaultPresignExpiry),
		},
		Worker: WorkerConfig{
			MaxConcurrentJobs: getEnvInt("MAX_CONCURRENT_JOBS", DefaultMaxConcurrentJobs),
			MetricsPort:       getEnvInt("METRICS_PORT", DefaultMetricsPort),
			TempDir:           getEnv("TEMP_DIR", os.TempDir()),
			JobTimeout:        getEnvDuration("JOB_TIMEOUT", DefaultJobTimeout),
		},
		Transcoding: TranscodingConfig{
			EnableLLHLS:     getEnvBool("ENABLE_LLHLS", true),
			EnableCMAF:      getEnvBool("ENABLE_CMAF", true),
			SegmentDuration: getEnvFloat("SEGMENT_DURATION", DefaultSegmentDuration),
			PartDuration:    getEnvFloat("PART_DURATION", DefaultPartDuration),
			UseShaka:        getEnvBool("USE_SHAKA", false),
			PresetsFile:     os.Getenv("PRESETS_FILE"),
			Presets:         DefaultPresets(),
		},
		CDN: CDNConfig{
			EnableMultiCDN:    getEnvBool("ENABLE_MULTI_CDN", false),
			PrimaryProvider:   getEnv("PRIMARY_CDN", "cloudfront"),
			HealthCheckPeriod: getEnvDuration("CDN_HEALTH_CHECK_PERIOD", DefaultHealthCheckPeriod),
			SessionTTL:        getEnvDuration("CDN_SESSION_TTL", DefaultSessionTTL),
			Strategy:          getEnv("CDN_STRATEGY", "weighted"),
		},
		DRM: DRMConfig{
			Enabled:             getEnvBool("ENABLE_DRM", false),
			Provider:            getEnv("DRM_PROVIDER", "local"),
			KeyServerURL:        os.Getenv("DRM_KEY_SERVER_URL"),
			APIKey:              os.Getenv("DRM_API_KEY"),
			WidevineLicenseURL:  os.Getenv("WIDEVINE_LICENSE_URL"),
			FairPlayLicenseURL:  os.Getenv("FAIRPLAY_LICENSE_URL"),
			PlayReadyLicenseURL: os.Getenv("PLAYREADY_LICENSE_URL"),
			FairPlayCertURL:     os.Getenv("FAIRPLAY_CERT_URL"),
			EncryptionScheme:    getEnv("ENCRYPTION_SCHEME", "cbcs"),
			KeyRotationPeriod:   getEnvDuration("KEY_ROTATION_PERIOD", 24*time.Hour),
		},
		SSAI: SSAIConfig{
			Enabled:                  getEnvBool("ENABLE_SSAI", false),
			AdDecisionServerURL:      os.Getenv("AD_DECISION_SERVER_URL"),
			PrerollAdServerURL:       os.Getenv("PREROLL_AD_SERVER_URL"),
			SlateAdURL:               os.Getenv("SLATE_AD_URL"),
			PersonalizationThreshold: getEnvInt("PERSONALIZATION_THRESHOLD", 100),
			MaxAdDuration:            getEnvInt("MAX_AD_DURATION", 120),
			MediaTailorConfigName:    os.Getenv("MEDIATAILOR_CONFIG_NAME"),
			EnableSCTE35Passthrough:  getEnvBool("ENABLE_SCTE35_PASSTHROUGH", true),
			PreserveSCTE35Markers:    getEnvBool("PRESERVE_SCTE35_MARKERS", true),
		},
		Observability: ObservabilityConfig{
			OTLPEndpoint:    getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", DefaultOTLPEndpoint),
			EnableTracing:   getEnvBool("ENABLE_TRACING", true),
			EnableMetrics:   getEnvBool("ENABLE_METRICS", true),
			ServiceName:     getEnv("OTEL_SERVICE_NAME", "hls-pipeline"),
			ServiceVersion:  getEnv("SERVICE_VERSION", "1.0.0"),
			TraceSampleRate: getEnvFloat("TRACE_SAMPLE_RATE", 0.1),
		},
		CORS: CORSConfig{
			AllowedOrigins: getEnvSlice("CORS_ALLOWED_ORIGINS", []string{"*"}),
			AllowedMethods: getEnvSlice("CORS_ALLOWED_METHODS", []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}),
			AllowedHeaders: getEnvSlice("CORS_ALLOWED_HEADERS", []string{"Content-Type", "Authorization", "X-Requested-With"}),
			MaxAge:         getEnvInt("CORS_MAX_AGE", 86400),
		},
	}

	if cfg.Transcoding.PresetsFile != "" {
		presetsFile, err := LoadPresets(cfg.Transcoding.PresetsFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load presets file: %w", err)
		}
		if len(presetsFile.Collections) > 0 {
			cfg.Transcoding.Presets = presetsFile.Collections[0].Presets
		}
	}

	if cfg.CDN.EnableMultiCDN {
		cfg.CDN.Providers = parseCDNProviders()
	}

	return cfg, nil
}

// LoadAPI loads configuration required for the API service.
func LoadAPI() (*Config, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.ValidateAPI(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadWorker loads configuration required for the Worker service.
func LoadWorker() (*Config, error) {
	cfg, err := Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.ValidateWorker(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ValidateAPI validates configuration required for the API service.
func (c *Config) ValidateAPI() error {
	var errs []string

	if c.AWS.RawBucket == "" {
		errs = append(errs, "S3_BUCKET is required")
	}
	if c.AWS.SQSQueueURL == "" {
		errs = append(errs, "SQS_QUEUE_URL is required")
	}
	if c.AWS.DynamoDBTable == "" {
		errs = append(errs, "DYNAMODB_TABLE is required")
	}
	if c.API.JWTSecret == "" {
		errs = append(errs, "JWT_SECRET is required")
	}
	if c.IsProduction() && len(c.API.JWTSecret) < 32 {
		errs = append(errs, "JWT_SECRET must be at least 32 characters in production")
	}

	if len(errs) > 0 {
		return errors.New("configuration errors: " + strings.Join(errs, ", "))
	}
	return nil
}

// ValidateWorker validates configuration required for the Worker service.
func (c *Config) ValidateWorker() error {
	var errs []string

	if c.AWS.RawBucket == "" {
		errs = append(errs, "S3_BUCKET is required")
	}
	if c.AWS.ProcessedBucket == "" {
		errs = append(errs, "PROCESSED_BUCKET is required")
	}
	if c.AWS.SQSQueueURL == "" {
		errs = append(errs, "SQS_QUEUE_URL is required")
	}
	if c.AWS.DynamoDBTable == "" {
		errs = append(errs, "DYNAMODB_TABLE is required")
	}
	if c.AWS.CDNDomain == "" {
		errs = append(errs, "CDN_DOMAIN is required")
	}

	if c.DRM.Enabled {
		if c.DRM.Provider != "local" && c.DRM.KeyServerURL == "" {
			errs = append(errs, "DRM_KEY_SERVER_URL is required when DRM is enabled with external provider")
		}
	}

	if c.SSAI.Enabled {
		if c.SSAI.AdDecisionServerURL == "" {
			errs = append(errs, "AD_DECISION_SERVER_URL is required when SSAI is enabled")
		}
	}

	if len(errs) > 0 {
		return errors.New("configuration errors: " + strings.Join(errs, ", "))
	}
	return nil
}

// IsProduction returns true if running in production environment.
func (c *Config) IsProduction() bool {
	env := strings.ToLower(c.Environment)
	return env == "prod" || env == "production"
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvInt64(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if floatVal, err := strconv.ParseFloat(value, 64); err == nil {
			return floatVal
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func getEnvSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		parts := strings.Split(value, ",")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	return defaultValue
}

func parseCDNProviders() []CDNProviderConfig {
	var providers []CDNProviderConfig

	for i := 1; i <= 5; i++ {
		prefix := fmt.Sprintf("CDN_PROVIDER_%d_", i)
		name := os.Getenv(prefix + "NAME")
		if name == "" {
			continue
		}

		provider := CDNProviderConfig{
			Name:       name,
			BaseURL:    os.Getenv(prefix + "URL"),
			Weight:     getEnvInt(prefix+"WEIGHT", 50),
			Priority:   getEnvInt(prefix+"PRIORITY", i),
			HealthURL:  os.Getenv(prefix + "HEALTH_URL"),
			SigningKey: os.Getenv(prefix + "SIGNING_KEY"),
			Region:     os.Getenv(prefix + "REGION"),
		}
		providers = append(providers, provider)
	}

	return providers
}
