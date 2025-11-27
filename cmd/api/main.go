package main

import (
	"context"
	"encoding/json"
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

	"github.com/amillerrr/hls-pipeline/internal/auth"
	"github.com/amillerrr/hls-pipeline/internal/handlers"
	"github.com/amillerrr/hls-pipeline/internal/logger"
	"github.com/amillerrr/hls-pipeline/internal/observability"
	"github.com/amillerrr/hls-pipeline/internal/storage"
)

func main() {
	log := logger.New()
	slog.SetDefault(log)

	if err := godotenv.Load(); err != nil {
		logger.Info(context.Background(), log, "No .env file found, relying on system ENV variables")
	}

	shutdownTracer := observability.InitTracer(context.Background(), "eye-api")
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracer(shutdownCtx); err != nil {
			logger.Error(context.Background(), log, "Failed to shutdown tracer", "error", err)
		}
	}()

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

	// Public endpoints
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/login", api.LoginHandler)
	mux.HandleFunc("/latest", api.GetLatestVideoHandler)

	// Protected endpoints
	mux.HandleFunc("/upload", auth.AuthMiddleware(api.UploadHandler))

	// Metrics endpoint
	mux.Handle("/metrics", localOnlyMiddleware(promhttp.Handler()))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      300 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
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
	ctxShut, cancelShut := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShut()

	if err := srv.Shutdown(ctxShut); err != nil {
		logger.Error(context.Background(), log, "Server forced to shutdown", "error", err)
	}

	logger.Info(context.Background(), log, "Server shutdown complete")
}

// ALB health check
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]string{
		"status":  "healthy",
		"service": "eye-api",
	}
	json.NewEncoder(w).Encode(response)
}

// Restrict access to localhost
func localOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteAddr := r.RemoteAddr

		// Allow localhost connections (sidecar)
		if isLocalRequest(remoteAddr) {
			next.ServeHTTP(w, r)
			return
		}

		if r.Header.Get("X-Forwarded-For") == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Deny non-local requests
		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}

// Check if request is from localhost
func isLocalRequest(remoteAddr string) bool {
	localPrefixes := []string{
		"127.0.0.1:",
		"localhost:",
		"[::1]:",
		// Private networks (ECS internal, VPC)
		"10.",
		"172.16.", "172.17.", "172.18.", "172.19.",
		"172.20.", "172.21.", "172.22.", "172.23.",
		"172.24.", "172.25.", "172.26.", "172.27.",
		"172.28.", "172.29.", "172.30.", "172.31.",
		"192.168.",
	}

	for _, prefix := range localPrefixes {
		if len(remoteAddr) >= len(prefix) && remoteAddr[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
