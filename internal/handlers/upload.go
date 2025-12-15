package handlers

import (
	"context"
	"encoding/json"
	"errors"
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

var tracer = otel.Tracer("hls-api")

// Configuration constants
const (
	PresignedURLExpiration = 10 * time.Minute
	MaxFilenameLength      = 255
	MaxListObjects         = 1000
	MaxRequestBodySize     = 1 << 20
)

// Allowed video extensions and content types
var (
	AllowedExtensions = map[string]bool{
		".mp4":  true,
		".mov":  true,
		".avi":  true,
		".mkv":  true,
		".webm": true,
	}

	AllowedContentTypes = map[string]bool{
		"video/mp4":        true,
		"video/quicktime":  true,
		"video/x-msvideo":  true,
		"video/x-matroska": true,
		"video/webm":       true,
	}
)

// Custom errors
var (
	ErrInvalidFileType    = errors.New("invalid file type")
	ErrFilenameTooLong    = errors.New("filename too long")
	ErrInvalidContentType = errors.New("invalid content type")
	ErrVideoNotFound      = errors.New("video not found")
)

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

// Handle CORS headers for all requests
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		// Handle preflight requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Write JSON response with error handling
func (a *API) writeJSON(ctx context.Context, w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error(ctx, a.log, "Failed to encode JSON response", "error", err)
	}
}

// Write an error response
func (a *API) writeError(ctx context.Context, w http.ResponseWriter, status int, message string) {
	a.writeJSON(ctx, w, status, map[string]string{"error": message})
}

// Wrap the request body with a size limit
func (a *API) limitedBodyReader(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodySize)
}

// Validate the upload filename
func validateFilename(filename string) error {
	if filename == "" {
		return errors.New("filename is required")
	}
	if len(filename) > MaxFilenameLength {
		return ErrFilenameTooLong
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if !AllowedExtensions[ext] {
		return fmt.Errorf("%w: allowed extensions are mp4, mov, avi, mkv, webm", ErrInvalidFileType)
	}

	return nil
}

// Validate the content type
func validateContentType(contentType string) error {
	if contentType == "" {
		return errors.New("content type is required")
	}
	if !AllowedContentTypes[contentType] {
		return fmt.Errorf("%w: %s", ErrInvalidContentType, contentType)
	}
	return nil
}

// Handle user authentication and return JWT token
func (a *API) LoginHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := auth.GetClientIP(r)

	// Check rate limiting before processing
	if auth.IsRateLimited(clientIP) {
		logger.Warn(ctx, a.log, "Rate limited login attempt", "ip", clientIP)
		a.writeError(ctx, w, http.StatusTooManyRequests, "Too many failed attempts, try again later")
		return
	}

	username, password, ok := r.BasicAuth()
	if !ok {
		auth.RecordAuthFailure(clientIP)
		a.writeError(ctx, w, http.StatusUnauthorized, "Missing credentials")
		return
	}

	expectedUsername := os.Getenv("API_USERNAME")
	expectedPassword := os.Getenv("API_PASSWORD")

	if expectedUsername == "" || expectedPassword == "" {
		env := os.Getenv("ENV")
		if env == "prod" || env == "production" {
			logger.Error(ctx, a.log, "CRITICAL: API_USERNAME or API_PASSWORD not set in production")
			a.writeError(ctx, w, http.StatusInternalServerError, "Server configuration error")
			return
		}
		// Development fallback with warning
		logger.Warn(ctx, a.log, "Using default credentials - DO NOT USE IN PRODUCTION")
		expectedUsername = "admin"
		expectedPassword = "secret"
	}

	if username != expectedUsername || password != expectedPassword {
		auth.RecordAuthFailure(clientIP)
		logger.Warn(ctx, a.log, "Failed login attempt", "username", username, "ip", clientIP)
		a.writeError(ctx, w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	token, err := auth.GenerateToken(username)
	if err != nil {
		logger.Error(ctx, a.log, "Failed to generate token", "error", err)
		a.writeError(ctx, w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	auth.ResetAuthAttempts(clientIP)

	logger.Info(ctx, a.log, "Successful login", "username", username, "ip", clientIP)
	a.writeJSON(ctx, w, http.StatusOK, map[string]string{"token": token})
}

// Request payload for upload initialization
type InitUploadRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
}

// Response payload for upload initialization
type InitUploadResponse struct {
	UploadURL string `json:"uploadUrl"`
	VideoID   string `json:"videoId"`
	Key       string `json:"key"`
}

// Generate Presigned URL
func (a *API) InitUploadHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		a.writeError(ctx, w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx, span := tracer.Start(ctx, "init-upload-handler",
		trace.WithAttributes(attribute.String("handler", "init-upload")))
	defer span.End()

	a.limitedBodyReader(w, r)

	var req InitUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		span.RecordError(err)
		// Check if it's a request too large error
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			a.writeError(ctx, w, http.StatusRequestEntityTooLarge, "Request body too large")
			return
		}
		a.writeError(ctx, w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate filename
	if err := validateFilename(req.Filename); err != nil {
		span.RecordError(err)
		a.writeError(ctx, w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate content type
	if err := validateContentType(req.ContentType); err != nil {
		span.RecordError(err)
		a.writeError(ctx, w, http.StatusBadRequest, err.Error())
		return
	}

	// Generate unique key
	videoID := uuid.New().String()
	ext := strings.ToLower(filepath.Ext(req.Filename))
	s3Key := fmt.Sprintf("uploads/%s%s", videoID, ext)
	bucket := os.Getenv("S3_BUCKET")

	span.SetAttributes(
		attribute.String("video.id", videoID),
		attribute.String("video.key", s3Key),
		attribute.String("video.content_type", req.ContentType),
	)

	// Generate Presigned URL
	url, err := a.s3Client.GeneratePresignedURL(ctx, bucket, s3Key, req.ContentType, PresignedURLExpiration)
	if err != nil {
		span.RecordError(err)
		logger.Error(ctx, a.log, "Failed to generate presigned URL", "error", err, "videoId", videoID)
		a.writeError(ctx, w, http.StatusInternalServerError, "Internal server error")
		return
	}

	logger.Info(ctx, a.log, "Generated presigned URL",
		"videoId", videoID,
		"key", s3Key,
		"filename", req.Filename,
	)

	a.writeJSON(ctx, w, http.StatusOK, InitUploadResponse{
		UploadURL: url,
		VideoID:   videoID,
		Key:       s3Key,
	})
}

// Request payload for completing an upload
type CompleteUploadRequest struct {
	VideoID  string `json:"videoId"`
	Key      string `json:"key"`
	Filename string `json:"filename"`
}

// Response payload for completed uploads
type CompleteUploadResponse struct {
	VideoID string `json:"videoId"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Verify the upload and queue the processing job
func (a *API) CompleteUploadHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		a.writeError(ctx, w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx, span := tracer.Start(ctx, "complete-upload-handler",
		trace.WithAttributes(attribute.String("handler", "complete-upload")))
	defer span.End()

	a.limitedBodyReader(w, r)

	var req CompleteUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		span.RecordError(err)
		var maxBytesErr *http.MaxBytesError
    if errors.As(err, &maxBytesErr) {
        a.writeError(ctx, w, http.StatusRequestEntityTooLarge, "Request body too large")
        return
    }
		a.writeError(ctx, w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate required fields
	if req.VideoID == "" {
		a.writeError(ctx, w, http.StatusBadRequest, "videoId is required")
		return
	}
	if req.Key == "" {
		a.writeError(ctx, w, http.StatusBadRequest, "key is required")
		return
	}

	span.SetAttributes(
		attribute.String("video.id", req.VideoID),
		attribute.String("video.key", req.Key),
	)

	bucket := os.Getenv("S3_BUCKET")

	// Verify file exists in S3 before queuing
	headResult, err := a.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(req.Key),
	})
	if err != nil {
		span.RecordError(err)
		logger.Warn(ctx, a.log, "File not found in S3 during completion",
			"key", req.Key,
			"videoId", req.VideoID,
			"error", err,
		)
		a.writeError(ctx, w, http.StatusNotFound, "Video file not found in S3")
		return
	}

	// Log file size for monitoring
	if headResult.ContentLength != nil {
		span.SetAttributes(attribute.Int64("video.size_bytes", *headResult.ContentLength))
		logger.Info(ctx, a.log, "Upload verified",
			"videoId", req.VideoID,
			"sizeBytes", *headResult.ContentLength,
		)
	}

	// Queue processing job
	message := map[string]string{
		"videoId":  req.VideoID,
		"s3Key":    req.Key,
		"bucket":   bucket,
		"filename": req.Filename,
	}

	messageBytes, err := json.Marshal(message)
	if err != nil {
		span.RecordError(err)
		logger.Error(ctx, a.log, "Failed to marshal message", "error", err, "videoId", req.VideoID)
		a.writeError(ctx, w, http.StatusInternalServerError, "Internal server error")
		return
	}

	_, err = a.sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(a.sqsQueueURL),
		MessageBody: aws.String(string(messageBytes)),
	})
	if err != nil {
		span.RecordError(err)
		logger.Error(ctx, a.log, "Failed to queue processing job",
			"error", err,
			"videoId", req.VideoID,
		)
		a.writeError(ctx, w, http.StatusInternalServerError, "Failed to queue job")
		return
	}

	logger.Info(ctx, a.log, "Processing job queued", "videoId", req.VideoID)

	a.writeJSON(ctx, w, http.StatusAccepted, CompleteUploadResponse{
		VideoID: req.VideoID,
		Status:  "processing",
		Message: "Video queued for processing",
	})
}

// Response payload for the latest video endpoint
type LatestVideoResponse struct {
	VideoID     string `json:"videoId"`
	PlaybackURL string `json:"playbackUrl"`
	ProcessedAt string `json:"processedAt"`
}

// Return the most recently processed video
func (a *API) GetLatestVideoHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodGet {
		a.writeError(ctx, w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx, span := tracer.Start(ctx, "get-latest-video")
	defer span.End()

	processedBucket := os.Getenv("PROCESSED_BUCKET")
	cdnDomain := os.Getenv("CDN_DOMAIN")

	if processedBucket == "" || cdnDomain == "" {
		logger.Error(ctx, a.log, "Missing required environment variables",
			"PROCESSED_BUCKET", processedBucket != "",
			"CDN_DOMAIN", cdnDomain != "",
		)
		a.writeError(ctx, w, http.StatusInternalServerError, "Server configuration error")
		return
	}

	// List objects to find the most recent
	result, err := a.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(processedBucket),
		Prefix:  aws.String("hls/"),
		MaxKeys: aws.Int32(MaxListObjects),
	})
	if err != nil {
		span.RecordError(err)
		logger.Error(ctx, a.log, "Failed to list processed videos", "error", err)
		a.writeError(ctx, w, http.StatusInternalServerError, "Failed to retrieve videos")
		return
	}

	if len(result.Contents) == 0 {
		a.writeError(ctx, w, http.StatusNotFound, "No processed videos found")
		return
	}

	// Find the most recent master.m3u8
	var latestKey string
	var latestTime time.Time
	for _, obj := range result.Contents {
		if obj.Key != nil && strings.HasSuffix(*obj.Key, "master.m3u8") {
			if obj.LastModified != nil && obj.LastModified.After(latestTime) {
				latestTime = *obj.LastModified
				latestKey = *obj.Key
			}
		}
	}

	if latestKey == "" {
		a.writeError(ctx, w, http.StatusNotFound, "No processed videos found")
		return
	}

	// Extract video ID from key (hls/{videoId}/master.m3u8)
	parts := strings.Split(latestKey, "/")
	videoID := ""
	if len(parts) >= 2 {
		videoID = parts[1]
	}

	span.SetAttributes(
		attribute.String("video.id", videoID),
		attribute.String("video.key", latestKey),
	)

	playbackURL := fmt.Sprintf("https://%s/%s", cdnDomain, latestKey)

	a.writeJSON(ctx, w, http.StatusOK, LatestVideoResponse{
		VideoID:     videoID,
		PlaybackURL: playbackURL,
		ProcessedAt: latestTime.Format(time.RFC3339),
	})
}
