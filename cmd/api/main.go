package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/joho/godotenv"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"

	"github.com/amillerrr/hls-pipeline/internal/api"
	"github.com/amillerrr/hls-pipeline/internal/auth"
	"github.com/amillerrr/hls-pipeline/internal/config"
	"github.com/amillerrr/hls-pipeline/internal/health"
	"github.com/amillerrr/hls-pipeline/internal/observability"
	"github.com/amillerrr/hls-pipeline/internal/storage"
)

const (
	ShutdownTimeout       = 30 * time.Second
	TracerShutdownTimeout = 5 * time.Second
	AWSConfigTimeout      = 10 * time.Second
)

func main() {
	// Initialize logger
	log := observability.NewLogger()
	slog.SetDefault(log)

	// Load .env file if present
	if err := godotenv.Load(); err != nil {
		log.Info("No .env file found, using system environment variables")
	}

	// Load configuration
	cfg, err := config.LoadAPI()
	if err != nil {
		log.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Initialize tracer
	shutdownTracer, err := observability.InitTracer(context.Background(), "hls-api", cfg)
	if err != nil {
		log.Error("Failed to initialize tracer", "error", err)
		os.Exit(1)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), TracerShutdownTimeout)
		defer cancel()
		if err := shutdownTracer(ctx); err != nil {
			log.Error("Failed to shutdown tracer", "error", err)
		}
	}()

	// Initialize AWS clients
	ctx, cancel := context.WithTimeout(context.Background(), AWSConfigTimeout)
	defer cancel()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWS.Region))
	if err != nil {
		log.Error("Failed to load AWS config", "error", err)
		os.Exit(1)
	}
	otelaws.AppendMiddlewares(&awsCfg.APIOptions)

	sqsClient := sqs.NewFromConfig(awsCfg)
	s3Client := storage.NewS3ClientFromAWSConfig(awsCfg)

	// Initialize video repository
	videoRepo, err := storage.NewVideoRepository(context.Background(), cfg)
	if err != nil {
		log.Error("Failed to initialize video repository", "error", err)
		os.Exit(1)
	}
	log.Info("DynamoDB video repository initialized")

	// Initialize JWT service
	jwtSecret, err := cfg.GetJWTSecret()
	if err != nil {
		log.Error("Failed to get JWT secret", "error", err)
		os.Exit(1)
	}
	jwtService, err := auth.NewJWTService(jwtSecret)
	if err != nil {
		log.Error("Failed to create JWT service", "error", err)
		os.Exit(1)
	}

	// Initialize rate limiter
	rateLimiter := auth.NewRateLimiter(auth.DefaultRateLimiterConfig())

	// Initialize health checker
	healthConfig := health.DefaultConfig("hls-api", log)
	healthConfig.S3Client = s3Client
	healthConfig.SQSClient = sqsClient
	healthConfig.S3Bucket = cfg.AWS.RawBucket
	healthConfig.SQSQueueURL = cfg.AWS.SQSQueueURL
	healthChecker := health.NewChecker(healthConfig)

	// Create and start server
	server, err := api.NewServer(&api.ServerConfig{
		Config:        cfg,
		Logger:        log,
		S3Client:      s3Client,
		SQSClient:     sqsClient,
		VideoRepo:     videoRepo,
		JWTService:    jwtService,
		RateLimiter:   rateLimiter,
		HealthChecker: healthChecker,
	})
	if err != nil {
		log.Error("Failed to create server", "error", err)
		os.Exit(1)
	}

	// Start server in goroutine
	go func() {
		if err := server.Start(); err != nil {
			log.Error("Server error", "error", err)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	// Graceful shutdown
	ctx, cancel = context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error("Server forced to shutdown", "error", err)
	}

	log.Info("Server shutdown complete")
}
