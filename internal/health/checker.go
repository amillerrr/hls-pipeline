package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// Configuration constants
const (
	DefaultCacheTTL       = 10 * time.Second
	DefaultCheckTimeout   = 5 * time.Second
	DefaultDeepCheckLimit = 10 * time.Second
)

// Status represents the health check response.
type Status struct {
	Status    string                    `json:"status"`
	Service   string                    `json:"service"`
	Timestamp string                    `json:"timestamp"`
	Checks    map[string]ComponentCheck `json:"checks,omitempty"`
}

// ComponentCheck represents the health of a single component.
type ComponentCheck struct {
	Status  string `json:"status"`
	Latency string `json:"latency,omitempty"`
	Error   string `json:"error,omitempty"`
}

// S3Client defines the S3 operations needed for health checks.
type S3Client interface {
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

// SQSClient defines the SQS operations needed for health checks.
type SQSClient interface {
	GetQueueAttributes(ctx context.Context, params *sqs.GetQueueAttributesInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
}

// Config holds health checker configuration.
type Config struct {
	ServiceName    string
	S3Client       S3Client
	SQSClient      SQSClient
	SQSQueueURL    string
	S3Bucket       string
	Logger         *slog.Logger
	CacheTTL       time.Duration
	CheckTimeout   time.Duration
	DeepCheckLimit time.Duration
}

// DefaultConfig returns a Config with default values.
func DefaultConfig(serviceName string, logger *slog.Logger) *Config {
	return &Config{
		ServiceName:    serviceName,
		Logger:         logger,
		CacheTTL:       DefaultCacheTTL,
		CheckTimeout:   DefaultCheckTimeout,
		DeepCheckLimit: DefaultDeepCheckLimit,
	}
}

// Checker provides health check functionality.
type Checker struct {
	config        *Config
	mu            sync.RWMutex
	lastCheck     time.Time
	lastStatus    *Status
	lastDeepCheck time.Time
}

// NewChecker creates a new health checker with the given configuration.
func NewChecker(config *Config) *Checker {
	return &Checker{
		config: config,
	}
}

// Check performs health checks on all dependencies.
// If deep is false, a cached result may be returned.
func (c *Checker) Check(ctx context.Context, deep bool) *Status {
	// Return cached result if available and not deep check
	if !deep {
		c.mu.RLock()
		if c.lastStatus != nil && time.Since(c.lastCheck) < c.config.CacheTTL {
			status := c.lastStatus
			c.mu.RUnlock()
			return status
		}
		c.mu.RUnlock()
	}

	status := &Status{
		Status:    "healthy",
		Service:   c.config.ServiceName,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Checks:    make(map[string]ComponentCheck),
	}

	// Only perform deep checks if requested
	if deep {
		// Check S3
		if c.config.S3Client != nil && c.config.S3Bucket != "" {
			s3Check := c.checkS3(ctx)
			status.Checks["s3"] = s3Check
			if s3Check.Status != "healthy" {
				status.Status = "degraded"
			}
		}

		// Check SQS
		if c.config.SQSClient != nil && c.config.SQSQueueURL != "" {
			sqsCheck := c.checkSQS(ctx)
			status.Checks["sqs"] = sqsCheck
			if sqsCheck.Status != "healthy" {
				status.Status = "degraded"
			}
		}
	}

	// Cache the result
	c.mu.Lock()
	c.lastCheck = time.Now()
	c.lastStatus = status
	c.mu.Unlock()

	return status
}

// CanPerformDeepCheck returns true if enough time has passed since the last deep check.
func (c *Checker) CanPerformDeepCheck() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return time.Since(c.lastDeepCheck) >= c.config.DeepCheckLimit
}

// RecordDeepCheck records the time of a deep health check.
func (c *Checker) RecordDeepCheck() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastDeepCheck = time.Now()
}

func (c *Checker) checkS3(ctx context.Context) ComponentCheck {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, c.config.CheckTimeout)
	defer cancel()

	_, err := c.config.S3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.config.S3Bucket),
	})

	latency := time.Since(start)

	if err != nil {
		return ComponentCheck{
			Status:  "unhealthy",
			Latency: latency.String(),
			Error:   err.Error(),
		}
	}

	return ComponentCheck{
		Status:  "healthy",
		Latency: latency.String(),
	}
}

func (c *Checker) checkSQS(ctx context.Context) ComponentCheck {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, c.config.CheckTimeout)
	defer cancel()

	_, err := c.config.SQSClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(c.config.SQSQueueURL),
		AttributeNames: []types.QueueAttributeName{
			types.QueueAttributeNameApproximateNumberOfMessages,
		},
	})

	latency := time.Since(start)

	if err != nil {
		return ComponentCheck{
			Status:  "unhealthy",
			Latency: latency.String(),
			Error:   err.Error(),
		}
	}

	return ComponentCheck{
		Status:  "healthy",
		Latency: latency.String(),
	}
}

// Handler returns an HTTP handler for basic health checks.
func (c *Checker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := c.Check(r.Context(), false)
		c.writeResponse(w, r, status)
	}
}

// DeepHandler returns an HTTP handler for deep health checks.
func (c *Checker) DeepHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !c.CanPerformDeepCheck() {
			// Return cached result if rate limited
			status := c.Check(r.Context(), false)
			status.Checks["rate_limited"] = ComponentCheck{
				Status: "info",
				Error:  "Deep health check rate limited, returning cached result",
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "10")
			w.WriteHeader(http.StatusTooManyRequests)

			if err := json.NewEncoder(w).Encode(status); err != nil && c.config.Logger != nil {
				c.config.Logger.Error("Failed to encode health check response", "error", err)
			}
			return
		}

		c.RecordDeepCheck()
		status := c.Check(r.Context(), true)
		c.writeResponse(w, r, status)
	}
}

func (c *Checker) writeResponse(w http.ResponseWriter, r *http.Request, status *Status) {
	w.Header().Set("Content-Type", "application/json")
	if status.Status != "healthy" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	if err := json.NewEncoder(w).Encode(status); err != nil && c.config.Logger != nil {
		c.config.Logger.Error("Failed to encode health check response", "error", err)
	}
}
