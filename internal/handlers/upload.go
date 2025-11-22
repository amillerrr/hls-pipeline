package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/amillerrr/hls-pipeline/internal/auth"
)

// Define a Counter Metric
var (
    uploadOps = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "video_upload_total",
            Help: "The total number of processed uploaded videos",
        },
        []string{"status"}, // Label by success/failure
    )
)

// APIHandler holds dependencies for our HTTP handlers
type APIHandler struct {
	S3Client *s3.Client
	Redis    *redis.Client
	Logger   *slog.Logger
}

// New creates a new APIHandler
func New(s3 *s3.Client, rdb *redis.Client, logger *slog.Logger) *APIHandler {
	return &APIHandler{
		S3Client: s3,
		Redis:    rdb,
		Logger:   logger,
	}
}

const MaxUploadSize = 500 << 20 // 500 MB Hard limit

// LoginHandler issues a token for valid credentials
func (h *APIHandler) LoginHandler(w http.ResponseWriter, r *http.Request) {
    // Mock Credentials
    // In Day 4/5, we could check a DB. For now, hardcode "admin" / "secret"
    user := r.FormValue("username")
    pass := r.FormValue("password")

    if user != "admin" || pass != "secret" {
        http.Error(w, "Invalid credentials", http.StatusUnauthorized)
        return
    }

    // Import your new auth package (ensure import path is correct)
    // e.g., "eye-of-storm/internal/auth"
    token, err := auth.GenerateToken(user)
    if err != nil {
        http.Error(w, "Signing Error", http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    w.Write([]byte(fmt.Sprintf(`{"token": "%s"}`, token)))
}

// UploadHandler handles the raw video ingest
func (h *APIHandler) UploadHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := uuid.New().String()

	// 1. Structured Logging Context
	logger := h.Logger.With(
		slog.String("req_id", requestID),
		slog.String("method", r.Method),
	)

	// 2. Method Enforcement
	if r.Method != http.MethodPost {
		uploadOps.WithLabelValues("error_method").Inc()
		logger.Warn("Method not allowed")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 3. Size Limitation (Security)
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadSize)
	if err := r.ParseMultipartForm(MaxUploadSize); err != nil {
		logger.Error("File too large or malformed body", "error", err)
		http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		logger.Error("Could not retrieve file from form", "error", err)
		http.Error(w, "Invalid file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 4. Magic Number Validation (Trust but Verify)
	// Read first 512 bytes to detect content type
	buff := make([]byte, 512)
	if _, err := file.Read(buff); err != nil {
		logger.Error("Failed to read file header", "error", err)
		http.Error(w, "Server Error", http.StatusInternalServerError)
		return
	}
	
	fileType := http.DetectContentType(buff)
	if fileType != "video/mp4" && fileType != "application/octet-stream" {
		logger.Warn("Invalid content type", "type", fileType)
		http.Error(w, "Invalid file format. Only MP4 allowed.", http.StatusBadRequest)
		return
	}

	// Reset file pointer after reading header
	if _, err := file.Seek(0, 0); err != nil {
		logger.Error("Failed to seek file", "error", err)
		http.Error(w, "Server Error", http.StatusInternalServerError)
		return
	}

	// 5. Storage (S3)
	fileUUID := uuid.New().String()
	safeFilename := fmt.Sprintf("%s%s", fileUUID, filepath.Ext(header.Filename))

	bucket := os.Getenv("S3_BUCKET")
	key := fmt.Sprintf("uploads/%s", safeFilename)

	_, err = h.S3Client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   file,
			ContentType: aws.String("video/mp4"),
	})

	if err != nil {
			logger.Error("Failed to upload to S3", "error", err)
			http.Error(w, "Upload Failed", http.StatusInternalServerError)
			return
	}

	// 6. Job Queue (Redis)
	job := map[string]string{
		"file_id": safeFilename,
	}
	payload, _ := json.Marshal(job)

	// Push to Redis
	err = h.Redis.LPush(context.Background(), "video_queue", payload).Err()
	if err != nil {
		logger.Error("Failed to queue job", "error", err)
		http.Error(w, "Queue Error", http.StatusInternalServerError)
		return
	}

	// 7. Success Response & Metrics
	duration := time.Since(start)
	logger.Info("Upload successful",
		slog.String("req_id", requestID),
		slog.String("filename", safeFilename),
		slog.Int64("size", header.Size),
		slog.Duration("duration", duration),
	)

	uploadOps.WithLabelValues("success").Inc()

	// FINAL WRITE - The only one that matters
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(fmt.Sprintf(`{"status": "processing", "id": "%s"}`, safeFilename)))
}
