package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"hls-pipeline/pkg/models"
)

var tracer = otel.Tracer("api-handlers")

// VideoRepository defines the interface for video metadata storage.
type VideoRepository interface {
	GetVideo(ctx context.Context, id string) (*models.Video, error)
	CreateVideo(ctx context.Context, video *models.Video) error
	UpdateVideo(ctx context.Context, video *models.Video) error
	GetLatestVideos(ctx context.Context, limit int) ([]*models.Video, error)
}

// Handlers contains all HTTP handlers and their dependencies.
type Handlers struct {
	s3Client   *s3.Client
	sqsClient  *sqs.Client
	videoRepo  VideoRepository
	config     *Config
	logger     *slog.Logger
}

// Config contains handler configuration.
type Config struct {
	RawBucket       string
	ProcessedBucket string
	QueueURL        string
	JWTSecret       string
	AllowedOrigins  []string
	MaxFileSize     int64
	PresignExpiry   time.Duration
}

// DefaultConfig returns the default handler configuration.
func DefaultConfig() *Config {
	return &Config{
		MaxFileSize:   10 * 1024 * 1024 * 1024, // 10GB
		PresignExpiry: 10 * time.Minute,
		AllowedOrigins: []string{"*"},
	}
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(s3Client *s3.Client, sqsClient *sqs.Client, videoRepo VideoRepository, config *Config, logger *slog.Logger) *Handlers {
	if config == nil {
		config = DefaultConfig()
	}
	return &Handlers{
		s3Client:  s3Client,
		sqsClient: sqsClient,
		videoRepo: videoRepo,
		config:    config,
		logger:    logger,
	}
}

// RegisterRoutes registers all routes with the router.
func (h *Handlers) RegisterRoutes(r *mux.Router) {
	// Upload routes
	r.HandleFunc("/upload/init", h.authMiddleware(h.InitiateUpload)).Methods(http.MethodPost)
	r.HandleFunc("/upload/complete", h.authMiddleware(h.CompleteUpload)).Methods(http.MethodPost)

	// Video routes
	r.HandleFunc("/videos", h.authMiddleware(h.GetLatestVideos)).Methods(http.MethodGet)
	r.HandleFunc("/videos/{id}", h.authMiddleware(h.GetVideo)).Methods(http.MethodGet)
	r.HandleFunc("/videos/{id}/status", h.authMiddleware(h.GetVideoStatus)).Methods(http.MethodGet)

	// Health check (no auth)
	r.HandleFunc("/health", h.HealthCheck).Methods(http.MethodGet)
}

// Allowed file extensions
var allowedExtensions = map[string]bool{
	".mp4":  true,
	".mov":  true,
	".avi":  true,
	".mkv":  true,
	".webm": true,
	".m4v":  true,
	".wmv":  true,
	".flv":  true,
}

// Allowed content types
var allowedContentTypes = map[string]bool{
	"video/mp4":        true,
	"video/quicktime":  true,
	"video/x-msvideo":  true,
	"video/x-matroska": true,
	"video/webm":       true,
	"video/x-m4v":      true,
	"video/x-ms-wmv":   true,
	"video/x-flv":      true,
}

// InitiateUploadRequest is the request body for initiating an upload.
type InitiateUploadRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
}

// InitiateUploadResponse is the response for initiating an upload.
type InitiateUploadResponse struct {
	UploadID    string `json:"uploadId"`
	UploadURL   string `json:"uploadUrl"`
	Key         string `json:"key"`
	ExpiresAt   string `json:"expiresAt"`
}

// InitiateUpload handles upload initialization and returns a presigned URL.
func (h *Handlers) InitiateUpload(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "initiate-upload")
	defer span.End()

	requestID := getRequestID(r)
	span.SetAttributes(attribute.String("request.id", requestID))

	var req InitiateUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.ErrorContext(ctx, "Failed to decode request", "error", err, "requestId", requestID)
		respondWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate filename
	if err := h.validateFilename(req.Filename); err != nil {
		h.logger.WarnContext(ctx, "Invalid filename", "error", err, "filename", req.Filename)
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate content type
	if !allowedContentTypes[req.ContentType] {
		h.logger.WarnContext(ctx, "Invalid content type", "contentType", req.ContentType)
		respondWithError(w, http.StatusBadRequest, "Invalid content type")
		return
	}

	// Validate file size
	if req.Size > h.config.MaxFileSize {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("File size exceeds maximum allowed (%d bytes)", h.config.MaxFileSize))
		return
	}

	// Generate upload ID and S3 key
	uploadID := uuid.New().String()
	ext := filepath.Ext(req.Filename)
	key := fmt.Sprintf("uploads/%s/%s%s", time.Now().Format("2006/01/02"), uploadID, ext)

	span.SetAttributes(
		attribute.String("upload.id", uploadID),
		attribute.String("s3.key", key),
		attribute.Int64("file.size", req.Size),
	)

	// Create presigned URL
	presignClient := s3.NewPresignClient(h.s3Client)
	presignResult, err := presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(h.config.RawBucket),
		Key:         aws.String(key),
		ContentType: aws.String(req.ContentType),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = h.config.PresignExpiry
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to create presigned URL", "error", err, "requestId", requestID)
		span.RecordError(err)
		respondWithError(w, http.StatusInternalServerError, "Failed to create upload URL")
		return
	}

	expiresAt := time.Now().Add(h.config.PresignExpiry)

	response := InitiateUploadResponse{
		UploadID:  uploadID,
		UploadURL: presignResult.URL,
		Key:       key,
		ExpiresAt: expiresAt.Format(time.RFC3339),
	}

	h.logger.InfoContext(ctx, "Upload initiated",
		"uploadId", uploadID,
		"filename", req.Filename,
		"size", req.Size,
		"requestId", requestID,
	)

	respondWithJSON(w, http.StatusOK, response)
}

// CompleteUploadRequest is the request body for completing an upload.
type CompleteUploadRequest struct {
	UploadID string `json:"uploadId"`
	Key      string `json:"key"`
	Title    string `json:"title,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// CompleteUploadResponse is the response for completing an upload.
type CompleteUploadResponse struct {
	VideoID string `json:"videoId"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// CompleteUpload handles upload completion and triggers processing.
func (h *Handlers) CompleteUpload(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "complete-upload")
	defer span.End()

	requestID := getRequestID(r)
	span.SetAttributes(attribute.String("request.id", requestID))

	var req CompleteUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.ErrorContext(ctx, "Failed to decode request", "error", err, "requestId", requestID)
		respondWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate S3 key format
	if err := h.validateS3Key(req.Key); err != nil {
		h.logger.WarnContext(ctx, "Invalid S3 key", "key", req.Key, "error", err)
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	span.SetAttributes(
		attribute.String("upload.id", req.UploadID),
		attribute.String("s3.key", req.Key),
	)

	// Verify the file exists in S3
	_, err := h.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(h.config.RawBucket),
		Key:    aws.String(req.Key),
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "File not found in S3", "key", req.Key, "error", err)
		span.RecordError(err)
		respondWithError(w, http.StatusNotFound, "Uploaded file not found")
		return
	}

	// Create video record if repository is available
	videoID := req.UploadID
	if h.videoRepo != nil {
		video := &models.Video{
			ID:        videoID,
			Title:     req.Title,
			Status:    "pending",
			S3Key:     req.Key,
			Metadata:  req.Metadata,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := h.videoRepo.CreateVideo(ctx, video); err != nil {
			h.logger.ErrorContext(ctx, "Failed to create video record", "error", err, "videoId", videoID)
			// Continue anyway - the SQS message is more important
		}
	}

	// Send message to SQS for processing
	message := map[string]interface{}{
		"videoId":   videoID,
		"s3Key":     req.Key,
		"bucket":    h.config.RawBucket,
		"title":     req.Title,
		"metadata":  req.Metadata,
		"timestamp": time.Now().Unix(),
	}
	messageBody, _ := json.Marshal(message)

	_, err = h.sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(h.config.QueueURL),
		MessageBody: aws.String(string(messageBody)),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"videoId": {
				DataType:    aws.String("String"),
				StringValue: aws.String(videoID),
			},
		},
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to queue processing job", "error", err, "videoId", videoID)
		span.RecordError(err)
		respondWithError(w, http.StatusInternalServerError, "Failed to queue processing job")
		return
	}

	h.logger.InfoContext(ctx, "Upload completed and queued",
		"videoId", videoID,
		"key", req.Key,
		"requestId", requestID,
	)

	response := CompleteUploadResponse{
		VideoID: videoID,
		Status:  "processing",
		Message: "Video uploaded successfully and queued for processing",
	}

	respondWithJSON(w, http.StatusOK, response)
}

// GetLatestVideos returns the most recent videos.
func (h *Handlers) GetLatestVideos(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "get-latest-videos")
	defer span.End()

	if h.videoRepo == nil {
		respondWithError(w, http.StatusServiceUnavailable, "Video repository not configured")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if l, err := parseInt(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	span.SetAttributes(attribute.Int("limit", limit))

	videos, err := h.videoRepo.GetLatestVideos(ctx, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get latest videos", "error", err)
		span.RecordError(err)
		respondWithError(w, http.StatusInternalServerError, "Failed to retrieve videos")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"videos": videos,
		"count":  len(videos),
	})
}

// GetVideo returns a specific video by ID.
func (h *Handlers) GetVideo(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "get-video")
	defer span.End()

	vars := mux.Vars(r)
	videoID := vars["id"]

	if videoID == "" {
		respondWithError(w, http.StatusBadRequest, "Video ID required")
		return
	}

	span.SetAttributes(attribute.String("video.id", videoID))

	if h.videoRepo == nil {
		respondWithError(w, http.StatusServiceUnavailable, "Video repository not configured")
		return
	}

	video, err := h.videoRepo.GetVideo(ctx, videoID)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get video", "error", err, "videoId", videoID)
		span.RecordError(err)
		respondWithError(w, http.StatusNotFound, "Video not found")
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

// GetVideoStatus returns the processing status of a video.
func (h *Handlers) GetVideoStatus(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "get-video-status")
	defer span.End()

	vars := mux.Vars(r)
	videoID := vars["id"]

	if videoID == "" {
		respondWithError(w, http.StatusBadRequest, "Video ID required")
		return
	}

	span.SetAttributes(attribute.String("video.id", videoID))

	if h.videoRepo == nil {
		// Return a placeholder status if no repo configured
		respondWithJSON(w, http.StatusOK, map[string]string{
			"videoId": videoID,
			"status":  "unknown",
		})
		return
	}

	video, err := h.videoRepo.GetVideo(ctx, videoID)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get video status", "error", err, "videoId", videoID)
		respondWithError(w, http.StatusNotFound, "Video not found")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"videoId":   video.ID,
		"status":    video.Status,
		"progress":  video.Progress,
		"updatedAt": video.UpdatedAt,
	})
}

// HealthCheck returns the health status of the API.
func (h *Handlers) HealthCheck(w http.ResponseWriter, r *http.Request) {
	respondWithJSON(w, http.StatusOK, map[string]string{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// Validation helpers

func (h *Handlers) validateFilename(filename string) error {
	if filename == "" {
		return models.ErrMissingRequiredField
	}

	if len(filename) > 255 {
		return models.ErrFilenameTooLong
	}

	// Prevent path traversal
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		return models.ErrInvalidKeyFormat
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if !allowedExtensions[ext] {
		return models.ErrInvalidFileType
	}

	return nil
}

var validKeyPattern = regexp.MustCompile(`^uploads/\d{4}/\d{2}/\d{2}/[a-f0-9-]+\.[a-z0-9]+$`)

func (h *Handlers) validateS3Key(key string) error {
	if key == "" {
		return models.ErrMissingRequiredField
	}

	// Prevent path traversal
	if strings.Contains(key, "..") {
		return models.ErrInvalidKeyFormat
	}

	if !validKeyPattern.MatchString(key) {
		return models.ErrInvalidKeyFormat
	}

	return nil
}

// Middleware

func (h *Handlers) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.config.JWTSecret == "" {
			// Skip auth if no secret configured (development mode)
			next(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			respondWithError(w, http.StatusUnauthorized, "Authorization header required")
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			respondWithError(w, http.StatusUnauthorized, "Invalid authorization format")
			return
		}

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return []byte(h.config.JWTSecret), nil
		})

		if err != nil || !token.Valid {
			respondWithError(w, http.StatusUnauthorized, "Invalid token")
			return
		}

		next(w, r)
	}
}

// Response helpers

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(payload)
}

func respondWithError(w http.ResponseWriter, code int, message string) {
	respondWithJSON(w, code, map[string]string{"error": message})
}

func getRequestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	return uuid.New().String()
}

func parseInt(s string) (int, error) {
	var i int
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}

// Import for SQS types
type types struct{}

// MessageAttributeValue placeholder - in real code, import from aws-sdk-go-v2/service/sqs/types
type MessageAttributeValue = sqs.MessageAttributeValue
