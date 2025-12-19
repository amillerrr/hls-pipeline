package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"

	"github.com/amillerrr/hls-pipeline/internal/auth"
	"github.com/amillerrr/hls-pipeline/internal/handlers"
	"github.com/amillerrr/hls-pipeline/internal/logger"
	"github.com/amillerrr/hls-pipeline/internal/observability"
	"github.com/amillerrr/hls-pipeline/internal/storage"
)

// Server configuration constants
const (
	DefaultPort           = "8080"
	ReadTimeout           = 30 * time.Second
	ReadHeaderTimeout     = 10 * time.Second
	WriteTimeout          = 300 * time.Second
	IdleTimeout           = 120 * time.Second
	MaxHeaderBytes        = 1 << 20 // 1 MB
	ShutdownTimeout       = 30 * time.Second
	TracerShutdownTimeout = 5 * time.Second
	AWSConfigTimeout      = 10 * time.Second
	HealthCheckTimeout    = 5 * time.Second
	DeepHealthRateLimit   = 10 * time.Second
)

// HealthChecker provides health check functionality
type HealthChecker struct {
	s3Client      *storage.Client
	sqsClient     *sqs.Client
	sqsQueueURL   string
	bucket        string
	log           *slog.Logger
	mu            sync.RWMutex
	lastCheck     time.Time
	lastStatus    *HealthStatus
	cacheTTL      time.Duration
	lastDeepCheck time.Time
}

// HealthStatus represents the health check response
type HealthStatus struct {
	Status    string                    `json:"status"`
	Service   string                    `json:"service"`
	Timestamp string                    `json:"timestamp"`
	Checks    map[string]ComponentCheck `json:"checks,omitempty"`
}

// ComponentCheck represents the health of a single component
type ComponentCheck struct {
	Status  string `json:"status"`
	Latency string `json:"latency,omitempty"`
	Error   string `json:"error,omitempty"`
}

var privateNetworks = []net.IPNet{
    // 10.0.0.0/8
    {IP: net.ParseIP("10.0.0.0"), Mask: net.CIDRMask(8, 32)},
    // 172.16.0.0/12
    {IP: net.ParseIP("172.16.0.0"), Mask: net.CIDRMask(12, 32)},
    // 192.168.0.0/16
    {IP: net.ParseIP("192.168.0.0"), Mask: net.CIDRMask(16, 32)},
    // localhost
    {IP: net.ParseIP("127.0.0.0"), Mask: net.CIDRMask(8, 32)},
}

// Create a new health checker
func NewHealthChecker(s3Client *storage.Client, sqsClient *sqs.Client, sqsQueueURL, bucket string, log *slog.Logger) *HealthChecker {
	return &HealthChecker{
		s3Client:    s3Client,
		sqsClient:   sqsClient,
		sqsQueueURL: sqsQueueURL,
		bucket:      bucket,
		log:         log,
		cacheTTL:    10 * time.Second, // Cache health check results for 10 seconds
	}
}

// Check performs health checks on all dependencies
func (h *HealthChecker) Check(ctx context.Context, deep bool) *HealthStatus {
	// Return cached result if available and not deep check
	if !deep {
		h.mu.RLock()
		if h.lastStatus != nil && time.Since(h.lastCheck) < h.cacheTTL {
			status := h.lastStatus
			h.mu.RUnlock()
			return status
		}
		h.mu.RUnlock()
	}

	status := &HealthStatus{
		Status:    "healthy",
		Service:   "hls-api",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Checks:    make(map[string]ComponentCheck),
	}

	// Only perform deep checks if requested
	if deep {
		// Check S3
		s3Check := h.checkS3(ctx)
		status.Checks["s3"] = s3Check
		if s3Check.Status != "healthy" {
			status.Status = "degraded"
		}

		// Check SQS
		sqsCheck := h.checkSQS(ctx)
		status.Checks["sqs"] = sqsCheck
		if sqsCheck.Status != "healthy" {
			status.Status = "degraded"
		}
	}

	// Cache the result
	h.mu.Lock()
	h.lastCheck = time.Now()
	h.lastStatus = status
	h.mu.Unlock()

	return status
}

// Check if enough time has passed since the last deep check
func (h *HealthChecker) CanPerformDeepCheck() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return time.Since(h.lastDeepCheck) >= DeepHealthRateLimit
}

// Record the time of a deep health check
func (h *HealthChecker) RecordDeepCheck() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastDeepCheck = time.Now()
}

func (h *HealthChecker) checkS3(ctx context.Context) ComponentCheck {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, HealthCheckTimeout)
	defer cancel()

	_, err := h.s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(h.bucket),
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

func (h *HealthChecker) checkSQS(ctx context.Context) ComponentCheck {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, HealthCheckTimeout)
	defer cancel()

	_, err := h.sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(h.sqsQueueURL),
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

func main() {
	log := logger.New()
	slog.SetDefault(log)

	if err := godotenv.Load(); err != nil {
		logger.Info(context.Background(), log, "No .env file found, relying on system ENV variables")
	}

	shutdownTracer, err := observability.InitTracer(context.Background(), "hls-api")
	if err != nil {
		logger.Error(context.Background(), log, "Failed to initialize tracer", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), TracerShutdownTimeout)
		defer cancel()
		if err := shutdownTracer(shutdownCtx); err != nil {
			logger.Error(context.Background(), log, "Failed to shutdown tracer", "error", err)
		}
	}()

	// Initialize AWS & SQS
	ctx, cancel := context.WithTimeout(context.Background(), AWSConfigTimeout)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		logger.Error(context.Background(), log, "Failed to load AWS config", "error", err)
		os.Exit(1)
	}
	otelaws.AppendMiddlewares(&cfg.APIOptions)
	sqsClient := sqs.NewFromConfig(cfg)

	// Initialize S3
	s3Client, err := storage.NewS3Client(ctx)
	if err != nil {
		logger.Error(context.Background(), log, "Could not connect to S3", "error", err)
		os.Exit(1)
	}

	// Validate required configuration
	sqsQueueURL := os.Getenv("SQS_QUEUE_URL")
	s3Bucket := os.Getenv("S3_BUCKET")
	if sqsQueueURL == "" || s3Bucket == "" {
		logger.Error(context.Background(), log, "Missing required environment variables",
			"SQS_QUEUE_URL", sqsQueueURL != "",
			"S3_BUCKET", s3Bucket != "",
		)
		os.Exit(1)
	}

	api := handlers.New(s3Client, sqsClient, sqsQueueURL, log)
	healthChecker := NewHealthChecker(s3Client, sqsClient, sqsQueueURL, s3Bucket, log)

	// Routing
	mux := http.NewServeMux()

	// Public endpoints
	mux.HandleFunc("/health", healthHandler(healthChecker))
	mux.HandleFunc("/health/deep", deepHealthHandler(healthChecker, log))
	mux.HandleFunc("/login", api.LoginHandler)
	mux.HandleFunc("/latest", api.GetLatestVideoHandler)

	// Protected endpoints
	mux.HandleFunc("/upload/init", auth.AuthMiddleware(api.InitUploadHandler))
	mux.HandleFunc("/upload/complete", auth.AuthMiddleware(api.CompleteUploadHandler))

	// Metrics endpoint
	mux.Handle("/metrics", internalOnlyMiddleware(promhttp.Handler()))

	// Apply CORS middleware to the entire mux
	handler := handlers.CORSMiddleware(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = DefaultPort
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadTimeout:       ReadTimeout,
		ReadHeaderTimeout: ReadHeaderTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
	}

	// Graceful Shutdown
	go func() {
		logger.Info(context.Background(), log, "Starting API Server", "port", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error(context.Background(), log, "Server error", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	logger.Info(context.Background(), log, "Shutting down server...")

	// Stop the rate limiter cleanup goroutine
	auth.StopRateLimiter()

	ctxShut, cancelShut := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancelShut()

	if err := srv.Shutdown(ctxShut); err != nil {
		logger.Error(context.Background(), log, "Server forced to shutdown", "error", err)
	}

	logger.Info(context.Background(), log, "Server shutdown complete")
}

// ALB health check
func healthHandler(checker *HealthChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := checker.Check(r.Context(), false)

		w.Header().Set("Content-Type", "application/json")
		if status.Status != "healthy" {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}

		if err := json.NewEncoder(w).Encode(status); err != nil {
			logger.Error(r.Context(), checker.log, "Failed to encode health check response", "error", err)
		}
	}
}

// Return a detailed health check with dependency status
func deepHealthHandler(checker *HealthChecker, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checker.CanPerformDeepCheck() {
			// Return cached result if rate limited
			status := checker.Check(r.Context(), false)
			status.Checks["rate_limited"] = ComponentCheck{
				Status: "info",
				Error:  "Deep health check rate limited, returning cached result",
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "10")
			w.WriteHeader(http.StatusTooManyRequests)

			if err := json.NewEncoder(w).Encode(status); err != nil {
				logger.Error(r.Context(), log, "Failed to encode health check response", "error", err)
			}
			return
		}

		checker.RecordDeepCheck()

		status := checker.Check(r.Context(), true)

		w.Header().Set("Content-Type", "application/json")
		if status.Status != "healthy" {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}

		if err := json.NewEncoder(w).Encode(status); err != nil {
			logger.Error(r.Context(), log, "Failed to encode health check response", "error", err)
		}
	}
}

// Restrict access to internal networks
func internalOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") != "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// Verify connection
		if isInternalRequest(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}

		// Deny non-local requests
		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}

// Check if request is from internal network
func isInternalRequest(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	} 

	for _, network := range privateNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return ip.IsLoopback()
}
