package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
	
	"github.com/amillerrr/hls-pipeline/internal/observability"
	"github.com/amillerrr/hls-pipeline/internal/handlers"
	"github.com/amillerrr/hls-pipeline/internal/storage" 
	"github.com/amillerrr/hls-pipeline/internal/logger" 
	"github.com/amillerrr/hls-pipeline/internal/auth" 
)

func main() {
	// Initialize Logger
	log := logger.New()
	slog.SetDefault(log)

	// Load .env
	if err := godotenv.Load(); err != nil {
		logger.Info(context.Background(), log, "No .env file found, relying on system ENV variables")
	} else {
		logger.Info(context.Background(), log, "Environment variables loaded from .env")
	}

	// Initialize Distributed Tracing
	shutdown := observability.InitTracer(context.Background(), "eye-api")
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			logger.Error(context.Background(), log, "Failed to shutdown tracer", "error", err)
		}
	}()

	// Initialize AWS & SQS
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		logger.Error(context.Background(), log, "Failed to load AWS config", "error", err)
		os.Exit(1)
	}
	otelaws.AppendMiddlewares(&cfg.APIOptions)
	sqsClient := sqs.NewFromConfig(cfg)
	
	queueURL := os.Getenv("SQS_QUEUE_URL")
	if queueURL == "" {
		logger.Error(context.Background(), log, "SQS_QUEUE_URL is not set")
		os.Exit(1)
	}

	// Initialize S3
	s3Client, err := storage.NewS3Client()
	if err != nil {
		logger.Error(context.Background(), log, "Could not connect to S3", "error", err)
		os.Exit(1)
	}

	// Dependency Injection
	api := handlers.New(s3Client, sqsClient, queueURL, log)

	// Routing
	mux := http.NewServeMux()
	mux.HandleFunc("/login", api.LoginHandler)
	mux.HandleFunc("/upload", auth.AuthMiddleware(api.UploadHandler))
	mux.HandleFunc("/latest", api.GetLatestVideoHandler)
	mux.Handle("/metrics", promhttp.Handler())

	// Start Server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	logger.Info(context.Background(), log, "Starting API Server", "port", port, "mode", "AWS_Hybrid")
	if err := srv.ListenAndServe(); err != nil {
		logger.Error(context.Background(), log, "Server failed", "error", err)
	}
}
