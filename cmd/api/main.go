package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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
	log := logger.New()
	slog.SetDefault(log)

	if err := godotenv.Load(); err != nil {
		logger.Info(context.Background(), log, "No .env file found, relying on system ENV variables")
	} 

	shutdownTracer := observability.InitTracer(context.Background(), "eye-api")
	defer shutdownTracer(context.Background())

	// Initialize AWS & SQS
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	api := handlers.New(s3Client, sqsClient, os.Getenv("SQS_QUEUE_URL"), log)

	// Routing
	mux := http.NewServeMux()
	mux.HandleFunc("/login", api.LoginHandler)
	mux.HandleFunc("/upload", auth.AuthMiddleware(api.UploadHandler))
	mux.HandleFunc("/latest", api.GetLatestVideoHandler)
	mux.Handle("/metrics", auth.AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
		promhttp.Handler().ServeHTTP(w, r)
	}))

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

	// Graceful Shutdown 
	go func() {
		logger.Info(context.Background(), log, "Starting API Server", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error(context.Background(), log, "Server error", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	logger.Info(context.Background(), log, "Shutting down server...")
	ctxShut, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShut()
	if err := srv.Shutdown(ctxShut); err != nil {
		logger.Error(context.Background(), log, "Server forced to shutdown", "error", err)
	}
}
