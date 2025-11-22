package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	
	"github.com/amillerrr/hls-pipeline/internal/handlers"
	"github.com/amillerrr/hls-pipeline/internal/storage" 
	"github.com/amillerrr/hls-pipeline/internal/auth" 
)

func main() {
	// 1. Logger Setup
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 2. Redis Connection
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379" // Default for local dev
	}
	
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	
	// Ping Redis to fail fast if down
	// In production, use a context with timeout
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logger.Error("Could not connect to Redis", "error", err)
		os.Exit(1)
	}

	// 3. S3 Connection
	s3Client, err := storage.NewS3Client()
	if err != nil {
		logger.Error("Could not connect to S3", "error", err)
		os.Exit(1)
	}

	// 4. Dependency Injection
	api := handlers.New(s3Client, rdb, logger)

	// 5. Routing
	// Using ServeMux 
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

	logger.Info("Starting API Server", "port", port)
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("Server failed", "error", err)
	}
}
