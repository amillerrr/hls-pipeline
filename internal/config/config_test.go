package config

import (
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	// Set required env vars for test
	os.Setenv("S3_BUCKET", "test-bucket")
	os.Setenv("PROCESSED_BUCKET", "test-processed")
	os.Setenv("SQS_QUEUE_URL", "https://sqs.test")
	os.Setenv("DYNAMODB_TABLE", "test-table")
	os.Setenv("CDN_DOMAIN", "cdn.test.com")
	defer func() {
		os.Unsetenv("S3_BUCKET")
		os.Unsetenv("PROCESSED_BUCKET")
		os.Unsetenv("SQS_QUEUE_URL")
		os.Unsetenv("DYNAMODB_TABLE")
		os.Unsetenv("CDN_DOMAIN")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.AWS.RawBucket != "test-bucket" {
		t.Errorf("RawBucket = %v, want %v", cfg.AWS.RawBucket, "test-bucket")
	}
}

func TestValidateAPI_MissingRequired(t *testing.T) {
	cfg := &Config{
		Environment: "dev",
		AWS:         AWSConfig{},
	}

	err := cfg.ValidateAPI()
	if err == nil {
		t.Error("ValidateAPI() expected error for missing required fields")
	}
}

func TestValidateAPI_ProductionRequiresCredentials(t *testing.T) {
	cfg := &Config{
		Environment: "production",
		AWS: AWSConfig{
			RawBucket:     "bucket",
			SQSQueueURL:   "url",
			DynamoDBTable: "table",
		},
		API: APIConfig{}, // Missing credentials
	}

	err := cfg.ValidateAPI()
	if err == nil {
		t.Error("ValidateAPI() expected error for missing credentials in production")
	}
}

func TestValidateWorker_MissingRequired(t *testing.T) {
	cfg := &Config{
		Environment: "dev",
		AWS:         AWSConfig{},
	}

	err := cfg.ValidateWorker()
	if err == nil {
		t.Error("ValidateWorker() expected error for missing required fields")
	}
}

func TestValidateWorker_AllPresent(t *testing.T) {
	cfg := &Config{
		Environment: "dev",
		AWS: AWSConfig{
			RawBucket:       "raw",
			ProcessedBucket: "processed",
			SQSQueueURL:     "url",
			CDNDomain:       "cdn.test",
			DynamoDBTable:   "table",
		},
	}

	err := cfg.ValidateWorker()
	if err != nil {
		t.Errorf("ValidateWorker() unexpected error = %v", err)
	}
}

func TestIsProduction(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"prod", true},
		{"production", true},
		{"PROD", true},
		{"PRODUCTION", true},
		{"dev", false},
		{"staging", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			cfg := &Config{Environment: tt.env}
			if got := cfg.IsProduction(); got != tt.want {
				t.Errorf("IsProduction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetAPICredentials_Development(t *testing.T) {
	cfg := &Config{
		Environment: "dev",
		API:         APIConfig{},
	}

	user, pass, err := cfg.GetAPICredentials()
	if err != nil {
		t.Fatalf("GetAPICredentials() error = %v", err)
	}
	if user != "admin" || pass != "secret" {
		t.Errorf("GetAPICredentials() = (%v, %v), want (admin, secret)", user, pass)
	}
}

func TestGetAPICredentials_Production(t *testing.T) {
	cfg := &Config{
		Environment: "production",
		API:         APIConfig{},
	}

	_, _, err := cfg.GetAPICredentials()
	if err == nil {
		t.Error("GetAPICredentials() expected error in production without credentials")
	}
}

func TestGetEnvSlice(t *testing.T) {
	os.Setenv("TEST_SLICE", "a, b, c")
	defer os.Unsetenv("TEST_SLICE")

	result := getEnvSlice("TEST_SLICE", nil)
	if len(result) != 3 {
		t.Errorf("getEnvSlice() len = %d, want 3", len(result))
	}
	if result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("getEnvSlice() = %v, want [a b c]", result)
	}
}

func TestGetEnvInt(t *testing.T) {
	os.Setenv("TEST_INT", "42")
	defer os.Unsetenv("TEST_INT")

	result := getEnvInt("TEST_INT", 10)
	if result != 42 {
		t.Errorf("getEnvInt() = %d, want 42", result)
	}

	// Test default
	result = getEnvInt("NONEXISTENT", 10)
	if result != 10 {
		t.Errorf("getEnvInt() = %d, want 10", result)
	}
}
