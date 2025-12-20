package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"

	"github.com/amillerrr/hls-pipeline/internal/config"
	"github.com/amillerrr/hls-pipeline/internal/observability"
	"github.com/amillerrr/hls-pipeline/internal/storage"
	"github.com/amillerrr/hls-pipeline/internal/transcoder"
	"github.com/amillerrr/hls-pipeline/internal/worker"
)

const (
	ShutdownTimeout       = 5 * time.Second
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
	cfg, err := config.LoadWorker()
	if err != nil {
		log.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Initialize tracer
	shutdownTracer, err := observability.InitTracer(context.Background(), "hls-worker", cfg)
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

	s3Client := s3.NewFromConfig(awsCfg)
	sqsClient := sqs.NewFromConfig(awsCfg)

	// Initialize video repository
	videoRepo, err := storage.NewVideoRepository(context.Background(), cfg)
	if err != nil {
		log.Error("Failed to initialize video repository", "error", err)
		os.Exit(1)
	}
	log.Info("DynamoDB video repository initialized")

	// Initialize transcoder
	transcoderCfg := transcoder.DefaultFFmpegConfig(log)
	tc := transcoder.NewTranscoder(transcoderCfg)

	// Create worker
	w := worker.New(&worker.Config{
		S3Client:   s3Client,
		SQSClient:  sqsClient,
		VideoRepo:  videoRepo,
		Transcoder: tc,
		AppConfig:  cfg,
		Logger:     log,
	})

	// Start metrics server
	metricsServer := startMetricsServer(cfg.Worker.MetricsPort, log)

	// Setup graceful shutdown
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-quit
		log.Info("Shutting down worker...")
		cancel()
	}()

	// Start polling
	w.Run(ctx)

	// Shutdown metrics server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer shutdownCancel()

	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		log.Error("Failed to shutdown metrics server", "error", err)
	}

	log.Info("Worker shutdown complete")
}

func startMetricsServer(port int, log *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	})

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("Starting metrics server", "port", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("Metrics server error", "error", err)
		}
	}()

	return server
}
