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
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	appconfig "github.com/amillerrr/hls-pipeline/internal/config"
	"github.com/amillerrr/hls-pipeline/internal/worker"
)

func main() {
	// Initialize structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: getLogLevel(),
	}))
	slog.SetDefault(logger)

	logger.Info("Starting HLS Pipeline Worker",
		"version", getEnv("SERVICE_VERSION", "dev"),
	)

	// Load configuration
	cfg, err := appconfig.LoadWorker()
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

	// Create worker
	w, err := worker.New(&worker.Config{
		S3Client:          s3Client,
		SQSClient:         sqsClient,
		DynamoDBClient:    dynamoClient,
		RawBucket:         cfg.AWS.RawBucket,
		ProcessedBucket:   cfg.AWS.ProcessedBucket,
		QueueURL:          cfg.AWS.SQSQueueURL,
		TableName:         cfg.AWS.DynamoDBTable,
		CDNDomain:         cfg.AWS.CDNDomain,
		MaxConcurrentJobs: cfg.Worker.MaxConcurrentJobs,
		TempDir:           cfg.Worker.TempDir,
		JobTimeout:        cfg.Worker.JobTimeout,
		TranscodingConfig: &worker.TranscodingConfig{
			EnableLLHLS:     cfg.Transcoding.EnableLLHLS,
			EnableCMAF:      cfg.Transcoding.EnableCMAF,
			SegmentDuration: cfg.Transcoding.SegmentDuration,
			PartDuration:    cfg.Transcoding.PartDuration,
			UseShaka:        cfg.Transcoding.UseShaka,
			Presets:         cfg.Transcoding.Presets,
		},
		DRMConfig: &worker.DRMConfig{
			Enabled:             cfg.DRM.Enabled,
			Provider:            cfg.DRM.Provider,
			KeyServerURL:        cfg.DRM.KeyServerURL,
			WidevineLicenseURL:  cfg.DRM.WidevineLicenseURL,
			FairPlayLicenseURL:  cfg.DRM.FairPlayLicenseURL,
			PlayReadyLicenseURL: cfg.DRM.PlayReadyLicenseURL,
			EncryptionScheme:    cfg.DRM.EncryptionScheme,
		},
		SSAIConfig: &worker.SSAIConfig{
			Enabled:             cfg.SSAI.Enabled,
			AdDecisionServerURL: cfg.SSAI.AdDecisionServerURL,
			MediaTailorConfig:   cfg.SSAI.MediaTailorConfigName,
		},
		Logger: logger,
	})
	if err != nil {
		logger.Error("Failed to create worker", "error", err)
		os.Exit(1)
	}

	// Start metrics server
	go startMetricsServer(cfg.Worker.MetricsPort, logger)

	// Create context that cancels on interrupt
	ctx, cancel := context.WithCancel(context.Background())

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("Received shutdown signal", "signal", sig)
		cancel()
	}()

	// Start worker
	logger.Info("Worker starting",
		"maxConcurrentJobs", cfg.Worker.MaxConcurrentJobs,
		"queueURL", cfg.AWS.SQSQueueURL,
	)

	if err := w.Start(ctx); err != nil {
		logger.Error("Worker failed", "error", err)
		os.Exit(1)
	}

	logger.Info("Worker stopped")
}

func startMetricsServer(port int, logger *slog.Logger) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	addr := ":" + string(rune(port))
	if port == 0 {
		addr = ":2112"
	} else {
		addr = ":" + itoa(port)
	}

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	logger.Info("Metrics server listening", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("Metrics server failed", "error", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	
	var b [20]byte
	pos := len(b)
	neg := i < 0
	if neg {
		i = -i
	}
	
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	
	if neg {
		pos--
		b[pos] = '-'
	}
	
	return string(b[pos:])
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

