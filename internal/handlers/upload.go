package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/amillerrr/hls-pipeline/internal/auth"
	"github.com/amillerrr/hls-pipeline/internal/logger"
	"github.com/amillerrr/hls-pipeline/internal/storage"
)

var tracer = otel.Tracer("eye-api")

type API struct {
	s3Client    *storage.Client
	sqsClient   *sqs.Client
	sqsQueueURL string
	log         *slog.Logger
}

func New(s3 *storage.Client, sqsClient *sqs.Client, sqsQueueURL string, log *slog.Logger) *API {
	return &API{
		s3Client:    s3,
		sqsClient:   sqsClient,
		sqsQueueURL: sqsQueueURL,
		log:         log,
	}
}

// Add CORS headers to the response
func (a *API) setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Max-Age", "86400")
}

// Handle user authentication and return JWT token
func (a *API) LoginHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a.setCORSHeaders(w, r)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	username, password, ok := r.BasicAuth()
	if !ok {
		http.Error(w, "Missing credentials", http.StatusUnauthorized)
		return
	}

	expectedUsername := os.Getenv("API_USERNAME")
	expectedPassword := os.Getenv("API_PASSWORD")

	if expectedUsername == "" || expectedPassword == "" {
		env := os.Getenv("ENV")
		if env == "prod" || env == "production" {
			logger.Error(ctx, a.log, "CRITICAL: API_USERNAME or API_PASSWORD not set in production")
			http.Error(w, "Server configuration error", http.StatusInternalServerError)
			return
		}
		// Development fallback with warning
		logger.Warn(ctx, a.log, "Using default credentials - DO NOT USE IN PRODUCTION")
		expectedUsername = "admin"
		expectedPassword = "secret"
	}

	if username != expectedUsername || password != expectedPassword {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := auth.GenerateToken(username)
	if err != nil {
		logger.Error(ctx, a.log, "Failed to generate token", "error", err)
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"token": token}); err != nil {
		logger.Error(ctx, a.log, "Failed to encode response", "error", err)
	}
}

// Request payload for Init
type InitUploadRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
}

// Response payload for Init
type InitUploadResponse struct {
	UploadURL string `json:"uploadUrl"`
	VideoID   string `json:"videoId"`
	Key       string `json:"key"`
}

// Generate Presigned URL
func (a *API) InitUploadHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a.setCORSHeaders(w, r)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, span := tracer.Start(ctx, "init-upload-handler",
		trace.WithAttributes(attribute.String("handler", "init-upload")))
	defer span.End()

	var req InitUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate file extension
	ext := strings.ToLower(filepath.Ext(req.Filename))
	allowedExts := map[string]bool{
		".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".webm": true,
	}
	if !allowedExts[ext] {
		http.Error(w, "Invalid file type. Allowed: mp4, mov, avi, mkv, webm", http.StatusBadRequest)
		return
	}

	// Generate unique key
	videoID := uuid.New().String()
	s3Key := fmt.Sprintf("uploads/%s%s", videoID, ext)
	bucket := os.Getenv("S3_BUCKET")

	// Generate Presigned URL 
	url, err := a.s3Client.GeneratePresignedURL(ctx, bucket, s3Key, req.ContentType, 15*time.Minute)
	if err != nil {
		logger.Error(ctx, a.log, "Failed to generate presigned URL", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	logger.Info(ctx, a.log, "Generated presigned URL", "videoId", videoID, "key", s3Key)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(InitUploadResponse{
		UploadURL: url,
		VideoID:   videoID,
		Key:       s3Key,
	}); err != nil {
    logger.Error(ctx, a.log, "Failed to encode response", "error", err)
	}
}

type CompleteUploadRequest struct {
	VideoID  string `json:"videoId"`
	Key      string `json:"key"`
	Filename string `json:"filename"`
}

// Queue the Job
func (a *API) CompleteUploadHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a.setCORSHeaders(w, r)

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, span := tracer.Start(ctx, "complete-upload-handler",
		trace.WithAttributes(attribute.String("handler", "complete-upload")))
	defer span.End()

	var req CompleteUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	bucket := os.Getenv("S3_BUCKET")

	// Verify file exists in S3 before queuing
	_, err := a.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(req.Key),
	})
	if err != nil {
		logger.Warn(ctx, a.log, "File not found in S3 during completion", "key", req.Key, "error", err)
		http.Error(w, "Video file not found in S3", http.StatusNotFound)
		return
	}

	// Queue processing job
	message := map[string]string{
		"videoId":  req.VideoID,
		"s3Key":    req.Key,
		"bucket":   bucket,
		"filename": req.Filename,
	}
	messageBytes, _ := json.Marshal(message)

	_, err = a.sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(a.sqsQueueURL),
		MessageBody: aws.String(string(messageBytes)),
	})
	if err != nil {
		logger.Error(ctx, a.log, "Failed to queue processing job", "error", err, "videoId", req.VideoID)
		http.Error(w, "Failed to queue job", http.StatusInternalServerError)
		return
	}

	logger.Info(ctx, a.log, "Processing job queued", "videoId", req.VideoID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"videoId": req.VideoID,
		"status":  "processing",
		"message": "Video queued for processing",
	}); err != nil {
		logger.Error(ctx, a.log, "Failed to encode response", "error", err)
	}
}

// Return the most recently processed video
func (a *API) GetLatestVideoHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a.setCORSHeaders(w, r)	

	// Handle CORS preflight
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, span := tracer.Start(ctx, "get-latest-video")
	defer span.End()

	processedBucket := os.Getenv("PROCESSED_BUCKET")
	cdnDomain := os.Getenv("CDN_DOMAIN")

	// List objects to find the most recent
	result, err := a.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(processedBucket),
		Prefix:  aws.String("hls/"),
		MaxKeys: aws.Int32(1000),
	})
	if err != nil {
		logger.Error(ctx, a.log, "Failed to list processed videos", "error", err)
		http.Error(w, "Failed to retrieve videos", http.StatusInternalServerError)
		return
	}

	if len(result.Contents) == 0 {
		http.Error(w, "No processed videos found", http.StatusNotFound)
		return
	}

	// Find the most recent master.m3u8
	var latestKey string
	var latestTime time.Time
	for _, obj := range result.Contents {
		if strings.HasSuffix(*obj.Key, "master.m3u8") {
			if obj.LastModified.After(latestTime) {
				latestTime = *obj.LastModified
				latestKey = *obj.Key
			}
		}
	}

	if latestKey == "" {
		http.Error(w, "No processed videos found", http.StatusNotFound)
		return
	}

	// Extract video ID from key (hls/{videoId}/master.m3u8)
	parts := strings.Split(latestKey, "/")
	videoID := ""
	if len(parts) >= 2 {
		videoID = parts[1]
	}

	playbackURL := fmt.Sprintf("https://%s/%s", cdnDomain, latestKey)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"videoId":     videoID,
		"playbackUrl": playbackURL,
		"processedAt": latestTime.Format(time.RFC3339),
	}); err != nil {
		logger.Error(ctx, a.log, "Failed to encode response", "error", err)
	}
}
