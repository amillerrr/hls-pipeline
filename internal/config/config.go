package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all application configuration.
type Config struct {
	Environment    string
	AWS            AWSConfig
	API            APIConfig
	Worker         WorkerConfig
	Observability  ObservabilityConfig
	CORS           CORSConfig
}

// AWSConfig holds AWS-specific configuration.
type AWSConfig struct {
	Region          string
	RawBucket       string
	ProcessedBucket string
	SQSQueueURL     string
	DynamoDBTable   string
	CDNDomain       string
}

// APIConfig holds API server configuration.
type APIConfig struct {
	Port      string
	Username  string
	Password  string
	JWTSecret string
}

// WorkerConfig holds worker-specific configuration.
type WorkerConfig struct {
	MaxConcurrentJobs int
	MetricsPort       int
}

// ObservabilityConfig holds observability configuration.
type ObservabilityConfig struct {
	OTLPEndpoint string
}

// CORSConfig holds CORS configuration.
type CORSConfig struct {
	AllowedOrigins []string
}

// Default values
const (
	DefaultPort              = "8080"
	DefaultMetricsPort       = 2112
	DefaultMaxConcurrentJobs = 1
	DefaultOTLPEndpoint      = "localhost:4317"
	DefaultRegion            = "us-west-2"
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
		},
		API: APIConfig{
			Port:      getEnv("PORT", DefaultPort),
			Username:  os.Getenv("API_USERNAME"),
			Password:  os.Getenv("API_PASSWORD"),
			JWTSecret: os.Getenv("JWT_SECRET"),
		},
		Worker: WorkerConfig{
			MaxConcurrentJobs: getEnvInt("MAX_CONCURRENT_JOBS", DefaultMaxConcurrentJobs),
			MetricsPort:       getEnvInt("METRICS_PORT", DefaultMetricsPort),
		},
		Observability: ObservabilityConfig{
			OTLPEndpoint: getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", DefaultOTLPEndpoint),
		},
		CORS: CORSConfig{
			AllowedOrigins: getEnvSlice("CORS_ALLOWED_ORIGINS", []string{
				"https://video.miller.today",
				"https://api.video.miller.today",
			}),
		},
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

	// In production, require explicit credentials
	if c.IsProduction() {
		if c.API.Username == "" {
			errs = append(errs, "API_USERNAME is required in production")
		}
		if c.API.Password == "" {
			errs = append(errs, "API_PASSWORD is required in production")
		}
		if c.API.JWTSecret == "" {
			errs = append(errs, "JWT_SECRET is required in production")
		}
		if len(c.API.JWTSecret) < 32 {
			errs = append(errs, "JWT_SECRET must be at least 32 characters in production")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("configuration errors: %s", strings.Join(errs, "; "))
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
	if c.AWS.CDNDomain == "" {
		errs = append(errs, "CDN_DOMAIN is required")
	}
	if c.AWS.DynamoDBTable == "" {
		errs = append(errs, "DYNAMODB_TABLE is required")
	}

	if len(errs) > 0 {
		return fmt.Errorf("configuration errors: %s", strings.Join(errs, "; "))
	}

	return nil
}

// IsProduction returns true if running in production environment.
func (c *Config) IsProduction() bool {
	env := strings.ToLower(c.Environment)
	return env == "prod" || env == "production"
}

// GetAPICredentials returns API credentials with fallback for development.
func (c *Config) GetAPICredentials() (username, password string, err error) {
	username = c.API.Username
	password = c.API.Password

	if username == "" || password == "" {
		if c.IsProduction() {
			return "", "", errors.New("API credentials not configured")
		}
		// Development fallback
		return "admin", "secret", nil
	}

	return username, password, nil
}

// GetJWTSecret returns the JWT secret with fallback for development.
func (c *Config) GetJWTSecret() ([]byte, error) {
	secret := c.API.JWTSecret

	if secret == "" {
		if c.IsProduction() {
			return nil, errors.New("JWT_SECRET not configured")
		}
		// Development fallback - still require explicit opt-in
		return nil, errors.New("JWT_SECRET is required (set it even for development)")
	}

	if len(secret) < 32 && c.IsProduction() {
		return nil, errors.New("JWT_SECRET must be at least 32 characters")
	}

	return []byte(secret), nil
}

// Helper functions

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil && intVal > 0 {
			return intVal
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
