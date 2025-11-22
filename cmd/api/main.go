package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	
	"github.com/amillerrr/hls-pipeline/internal/handlers"
	"github.com/amillerrr/hls-pipeline/internal/storage" 
	"github.com/amillerrr/hls-pipeline/internal/auth" 
)

func main() {
	// 1. Load .env (Local Dev)
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, relying on system ENV")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 2. Init AWS & SQS
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		logger.Error("Failed to load AWS config", "error", err)
		os.Exit(1)
	}
	sqsClient := sqs.NewFromConfig(cfg)
	
	queueURL := os.Getenv("SQS_QUEUE_URL")
	if queueURL == "" {
		logger.Error("SQS_QUEUE_URL is not set")
		os.Exit(1)
	}

	// 3. Init S3
	s3Client, err := storage.NewS3Client()
	if err != nil {
		logger.Error("Could not connect to S3", "error", err)
		os.Exit(1)
	}

	// 4. Dependency Injection
	// We no longer pass Redis; we pass SQS + QueueURL
	api := handlers.New(s3Client, sqsClient, queueURL, logger)

	// 5. Routing
	mux := http.NewServeMux()
	mux.HandleFunc("/login", api.LoginHandler)
	mux.HandleFunc("/upload", auth.AuthMiddleware(api.UploadHandler))
	mux.Handle("/metrics", promhttp.Handler())

	// 6. Server Start
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

	logger.Info("Starting API Server", "port", port, "mode", "AWS_Hybrid")
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("Server failed", "error", err)
	}
}
