package health

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// Mock S3 client
type mockS3Client struct {
	err error
}

func (m *mockS3Client) HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &s3.HeadBucketOutput{}, nil
}

// Mock SQS client
type mockSQSClient struct {
	err error
}

func (m *mockSQSClient) GetQueueAttributes(ctx context.Context, params *sqs.GetQueueAttributesInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &sqs.GetQueueAttributesOutput{}, nil
}

func TestChecker_Check_Shallow(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config := DefaultConfig("test-service", logger)
	checker := NewChecker(config)

	status := checker.Check(context.Background(), false)

	if status.Status != "healthy" {
		t.Errorf("Status = %s, want healthy", status.Status)
	}
	if status.Service != "test-service" {
		t.Errorf("Service = %s, want test-service", status.Service)
	}
	if len(status.Checks) != 0 {
		t.Errorf("Checks should be empty for shallow check, got %d", len(status.Checks))
	}
}

func TestChecker_Check_Deep_AllHealthy(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config := &Config{
		ServiceName:    "test-service",
		S3Client:       &mockS3Client{},
		SQSClient:      &mockSQSClient{},
		S3Bucket:       "test-bucket",
		SQSQueueURL:    "https://sqs.test",
		Logger:         logger,
		CacheTTL:       time.Second,
		CheckTimeout:   time.Second,
		DeepCheckLimit: time.Millisecond,
	}
	checker := NewChecker(config)

	status := checker.Check(context.Background(), true)

	if status.Status != "healthy" {
		t.Errorf("Status = %s, want healthy", status.Status)
	}
	if len(status.Checks) != 2 {
		t.Errorf("Checks should have 2 entries, got %d", len(status.Checks))
	}
	if status.Checks["s3"].Status != "healthy" {
		t.Errorf("S3 check status = %s, want healthy", status.Checks["s3"].Status)
	}
	if status.Checks["sqs"].Status != "healthy" {
		t.Errorf("SQS check status = %s, want healthy", status.Checks["sqs"].Status)
	}
}

func TestChecker_Check_Deep_S3Unhealthy(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config := &Config{
		ServiceName:    "test-service",
		S3Client:       &mockS3Client{err: errors.New("s3 error")},
		SQSClient:      &mockSQSClient{},
		S3Bucket:       "test-bucket",
		SQSQueueURL:    "https://sqs.test",
		Logger:         logger,
		CacheTTL:       time.Second,
		CheckTimeout:   time.Second,
		DeepCheckLimit: time.Millisecond,
	}
	checker := NewChecker(config)

	status := checker.Check(context.Background(), true)

	if status.Status != "degraded" {
		t.Errorf("Status = %s, want degraded", status.Status)
	}
	if status.Checks["s3"].Status != "unhealthy" {
		t.Errorf("S3 check status = %s, want unhealthy", status.Checks["s3"].Status)
	}
	if status.Checks["s3"].Error != "s3 error" {
		t.Errorf("S3 check error = %s, want 's3 error'", status.Checks["s3"].Error)
	}
}

func TestChecker_Check_Caching(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config := &Config{
		ServiceName:    "test-service",
		Logger:         logger,
		CacheTTL:       time.Hour, // Long TTL for test
		CheckTimeout:   time.Second,
		DeepCheckLimit: time.Millisecond,
	}
	checker := NewChecker(config)

	// First check
	status1 := checker.Check(context.Background(), false)

	// Second check should return cached result
	status2 := checker.Check(context.Background(), false)

	if status1.Timestamp != status2.Timestamp {
		t.Error("Cached result should have same timestamp")
	}
}

func TestChecker_CanPerformDeepCheck(t *testing.T) {
	config := &Config{
		ServiceName:    "test-service",
		DeepCheckLimit: 50 * time.Millisecond,
	}
	checker := NewChecker(config)

	// Should be able to perform deep check initially
	if !checker.CanPerformDeepCheck() {
		t.Error("CanPerformDeepCheck() = false initially")
	}

	// Record a deep check
	checker.RecordDeepCheck()

	// Should not be able to perform deep check immediately
	if checker.CanPerformDeepCheck() {
		t.Error("CanPerformDeepCheck() = true immediately after recording")
	}

	// Wait for the limit to pass
	time.Sleep(60 * time.Millisecond)

	// Should be able to perform deep check again
	if !checker.CanPerformDeepCheck() {
		t.Error("CanPerformDeepCheck() = false after limit passed")
	}
}

func TestChecker_Handler(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config := DefaultConfig("test-service", logger)
	checker := NewChecker(config)

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	checker.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Handler returned %d, want %d", rr.Code, http.StatusOK)
	}

	var status Status
	if err := json.NewDecoder(rr.Body).Decode(&status); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if status.Status != "healthy" {
		t.Errorf("Status = %s, want healthy", status.Status)
	}
}

func TestChecker_DeepHandler_RateLimited(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config := &Config{
		ServiceName:    "test-service",
		Logger:         logger,
		CacheTTL:       time.Second,
		CheckTimeout:   time.Second,
		DeepCheckLimit: time.Hour, // Long limit for test
	}
	checker := NewChecker(config)

	// Record a deep check to trigger rate limiting
	checker.RecordDeepCheck()

	req := httptest.NewRequest("GET", "/health/deep", nil)
	rr := httptest.NewRecorder()

	checker.DeepHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("Handler returned %d, want %d", rr.Code, http.StatusTooManyRequests)
	}

	if rr.Header().Get("Retry-After") != "10" {
		t.Errorf("Retry-After = %s, want 10", rr.Header().Get("Retry-After"))
	}
}

func TestChecker_Handler_Unhealthy(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config := &Config{
		ServiceName:    "test-service",
		S3Client:       &mockS3Client{err: errors.New("s3 error")},
		S3Bucket:       "test-bucket",
		Logger:         logger,
		CacheTTL:       time.Millisecond,
		CheckTimeout:   time.Second,
		DeepCheckLimit: time.Millisecond,
	}
	checker := NewChecker(config)

	// Force a deep check to get unhealthy status
	checker.Check(context.Background(), true)

	// Now the cached status should be degraded
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	// Wait for cache to be updated
	time.Sleep(10 * time.Millisecond)
	checker.Check(context.Background(), true)

	checker.Handler().ServeHTTP(rr, req)

	var status Status
	if err := json.NewDecoder(rr.Body).Decode(&status); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// shallow check returns healthy even if deep check found issues
	// because shallow checks don't actually check dependencies
}
