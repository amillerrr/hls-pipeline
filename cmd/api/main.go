package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/amillerrr/hls-pipeline/internal/api"
	appconfig "github.com/amillerrr/hls-pipeline/internal/config"
)

func main() {
	// Initialize structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: getLogLevel(),
	}))
	slog.SetDefault(logger)

	logger.Info("Starting HLS Pipeline API",
		"version", getEnv("SERVICE_VERSION", "dev"),
	)

	// Load configuration
	cfg, err := appconfig.LoadAPI()
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Initialize AWS SDK
	ctx := context.Background()
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.AWS.Region),
	)
	if err != nil {
		logger.Error("Failed to load AWS config", "error", err)
		os.Exit(1)
	}

	// Create AWS clients
	s3Client := s3.NewFromConfig(awsCfg)
	sqsClient := sqs.NewFromConfig(awsCfg)
	dynamoClient := dynamodb.NewFromConfig(awsCfg)

	// Create API handler
	handler, err := api.NewHandler(&api.HandlerConfig{
		S3Client:        s3Client,
		SQSClient:       sqsClient,
		DynamoDBClient:  dynamoClient,
		RawBucket:       cfg.AWS.RawBucket,
		ProcessedBucket: cfg.AWS.ProcessedBucket,
		QueueURL:        cfg.AWS.SQSQueueURL,
		TableName:       cfg.AWS.DynamoDBTable,
		CDNDomain:       cfg.AWS.CDNDomain,
		MaxUploadSize:   cfg.API.MaxUploadSize,
		PresignExpiry:   cfg.API.PresignExpiry,
		JWTSecret:       cfg.API.JWTSecret,
		Logger:          logger,
	})
	if err != nil {
		logger.Error("Failed to create API handler", "error", err)
		os.Exit(1)
	}

	// Create router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(api.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// CORS
	r.Use(api.CORSMiddleware(cfg.CORS))

	// Health endpoints (no auth)
	r.Get("/health", handler.Health)
	r.Get("/ready", handler.Ready)

	// Metrics endpoint
	r.Handle("/metrics", promhttp.Handler())

	// API routes
	r.Route("/api/v1", func(r chi.Router) {
		// Public routes
		r.Group(func(r chi.Router) {
			r.Post("/auth/token", handler.GenerateToken)
		})

		// Protected routes
		r.Group(func(r chi.Router) {
			r.Use(api.JWTAuthMiddleware(cfg.API.JWTSecret))

			// Video management
			r.Route("/videos", func(r chi.Router) {
				r.Get("/", handler.ListVideos)
				r.Post("/", handler.CreateVideo)
				r.Post("/upload", handler.InitiateUpload)
				r.Post("/upload/multipart", handler.InitiateMultipartUpload)
				r.Post("/upload/multipart/complete", handler.CompleteMultipartUpload)

				r.Route("/{videoID}", func(r chi.Router) {
					r.Get("/", handler.GetVideo)
					r.Put("/", handler.UpdateVideo)
					r.Delete("/", handler.DeleteVideo)
					r.Get("/status", handler.GetVideoStatus)
					r.Post("/reprocess", handler.ReprocessVideo)
				})
			})

			// Stats
			r.Get("/stats", handler.GetStats)
		})
	})

	// Create server
	addr := fmt.Sprintf(":%s", cfg.API.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		logger.Info("API server listening", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("Server stopped")
}

func getLogLevel() slog.Level {
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

