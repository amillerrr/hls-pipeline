package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/amillerrr/hls-pipeline/internal/live"
)

func main() {
	// Initialize logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: getLogLevel(),
	}))
	slog.SetDefault(logger)

	logger.Info("starting live streaming service")

	// Load configuration
	cfg := loadConfig()

	// Load AWS config
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.AWSRegion),
	)
	if err != nil {
		logger.Error("failed to load AWS config", "error", err)
		os.Exit(1)
	}

	// Handle custom endpoint for LocalStack
	if endpoint := os.Getenv("AWS_ENDPOINT_URL"); endpoint != "" {
		awsCfg.BaseEndpoint = &endpoint
	}

	// Create MediaMTX client
	var mediamtxClient *live.MediaMTXClient
	if cfg.MediaMTXURL != "" {
		mediamtxClient = live.NewMediaMTXClient(&live.MediaMTXConfig{
			BaseURL: fmt.Sprintf("http://%s:%d", cfg.MediaMTXURL, cfg.MediaMTXAPIPort),
			APIPort: cfg.MediaMTXAPIPort,
		}, logger)
		logger.Info("MediaMTX client configured", "url", cfg.MediaMTXURL)
	}

	// Create stream manager
	streamManager := live.NewStreamManager(&live.ManagerConfig{
		MaxStreams:          cfg.MaxStreams,
		DefaultPresets:      []string{"1080p", "720p", "480p"},
		OutputBucket:        cfg.OutputBucket,
		OutputPrefix:        "live",
		HealthCheckInterval: 10 * time.Second,
		StatsInterval:       5 * time.Second,
		MediaMTXURL:         cfg.MediaMTXURL,
		MediaMTXAPIPort:     cfg.MediaMTXAPIPort,
		AWSRegion:           cfg.AWSRegion,
		CDNDomain:           cfg.CDNDomain,
	}, logger)

	// Create SRT ingest handler
	srtHandler := live.NewSRTIngestHandler(
		&live.SRTConfig{
			ListenPort: cfg.SRTPortRangeStart,
			Latency:    200,
			Mode:       "listener",
		},
		streamManager,
		mediamtxClient,
		logger,
	)

	// Create API handler
	apiHandler := live.NewAPIHandler(streamManager, srtHandler, logger)

	// Setup router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// Health check endpoints
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		health := apiHandler.HealthCheck(r.Context())
		if health["status"] == "healthy" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		// Write JSON response
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"%s","activeStreams":%d}`,
			health["status"],
			health["activeStreams"],
		)
	})

	// Register API routes
	r.Route("/api/v1", func(r chi.Router) {
		apiHandler.RegisterRoutes(r)
	})

	// Create HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server
	go func() {
		logger.Info("server listening", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down server...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop all active streams
	for _, stream := range streamManager.ListStreams() {
		if stream.State == live.StreamStateActive {
			logger.Info("stopping stream", "streamId", stream.ID)
			streamManager.StopStream(ctx, stream.ID)
		}
	}

	// Shutdown HTTP server
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	logger.Info("server stopped")
}

// Config holds the service configuration.
type Config struct {
	Port              int
	AWSRegion         string
	OutputBucket      string
	CDNDomain         string
	MediaMTXURL       string
	MediaMTXAPIPort   int
	SRTPortRangeStart int
	SRTPortRangeEnd   int
	MaxStreams        int
}

func loadConfig() *Config {
	return &Config{
		Port:              getEnvInt("PORT", 8080),
		AWSRegion:         getEnv("AWS_REGION", "us-west-2"),
		OutputBucket:      getEnv("OUTPUT_BUCKET", "hls-pipeline-processed"),
		CDNDomain:         getEnv("CDN_DOMAIN", ""),
		MediaMTXURL:       getEnv("MEDIAMTX_URL", "localhost"),
		MediaMTXAPIPort:   getEnvInt("MEDIAMTX_API_PORT", 9997),
		SRTPortRangeStart: getEnvInt("SRT_PORT_RANGE_START", 9000),
		SRTPortRangeEnd:   getEnvInt("SRT_PORT_RANGE_END", 9100),
		MaxStreams:        getEnvInt("MAX_STREAMS", 10),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

func getLogLevel() slog.Level {
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

