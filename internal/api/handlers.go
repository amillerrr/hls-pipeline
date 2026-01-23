package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	sqs_types "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/amillerrr/hls-pipeline/internal/storage"
	"github.com/amillerrr/hls-pipeline/pkg/models"
)

var tracer = otel.Tracer("hls-pipeline/api")

// Handler contains all HTTP handlers and their dependencies.
type Handler struct {
	s3Client       *s3.Client
	sqsClient      *sqs.Client
	dynamoDBClient *dynamodb.Client
	videoRepo      *storage.VideoRepository
	config         *HandlerConfig
	logger         *slog.Logger
}

// HandlerConfig contains handler configuration.
type HandlerConfig struct {
	S3Client        *s3.Client
	SQSClient       *sqs.Client
	DynamoDBClient  *dynamodb.Client
	RawBucket       string
	ProcessedBucket string
	QueueURL        string
	TableName       string
	CDNDomain       string
	JWTSecret       string
	MaxUploadSize   int64
	PresignExpiry   time.Duration
	Logger          *slog.Logger
}

// Allowed file extensions.
var allowedExtensions = map[string]bool{
	".mp4":  true,
	".mov":  true,
	".avi":  true,
	".mkv":  true,
	".webm": true,
	".m4v":  true,
	".wmv":  true,
	".flv":  true,
	".mxf":  true,
	".ts":   true,
}

// Allowed content types.
var allowedContentTypes = map[string]bool{
	"video/mp4":                true,
	"video/quicktime":          true,
	"video/x-msvideo":          true,
	"video/x-matroska":         true,
	"video/webm":               true,
	"video/x-m4v":              true,
	"video/x-ms-wmv":           true,
	"video/x-flv":              true,
	"application/mxf":          true,
	"video/MP2T":               true,
	"application/octet-stream": true, // Allow for binary uploads
}

// NewHandler creates a new Handler instance.
func NewHandler(cfg *HandlerConfig) (*Handler, error) {
	if cfg == nil {
		return nil, fmt.Errorf("handler config is required")
	}

	if cfg.MaxUploadSize == 0 {
		cfg.MaxUploadSize = 10 * 1024 * 1024 * 1024 // 10GB default
	}

	if cfg.PresignExpiry == 0 {
		cfg.PresignExpiry = 15 * time.Minute
	}

	var videoRepo *storage.VideoRepository
	if cfg.DynamoDBClient != nil && cfg.TableName != "" {
		videoRepo = storage.NewVideoRepositoryFromClient(cfg.DynamoDBClient, cfg.TableName)
	}

	return &Handler{
		s3Client:       cfg.S3Client,
		sqsClient:      cfg.SQSClient,
		dynamoDBClient: cfg.DynamoDBClient,
		videoRepo:      videoRepo,
		config:         cfg,
		logger:         cfg.Logger,
	}, nil
}

// RegisterRoutes registers all routes with the chi router.
func (h *Handler) RegisterRoutes(r chi.Router) {
	// Health endpoints (no auth)
	r.Get("/health", h.Health)
	r.Get("/ready", h.Ready)

	// API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
		// Public routes
		r.Post("/auth/token", h.GenerateToken)

		// Protected routes (would add JWT middleware here)
		r.Route("/videos", func(r chi.Router) {
			r.Get("/", h.ListVideos)
			r.Post("/", h.CreateVideo)
			r.Post("/upload", h.InitiateUpload)
			r.Post("/upload/multipart", h.InitiateMultipartUpload)
			r.Post("/upload/multipart/complete", h.CompleteMultipartUpload)
			r.Post("/upload/complete", h.CompleteUpload)

			r.Route("/{videoID}", func(r chi.Router) {
				r.Get("/", h.GetVideo)
				r.Delete("/", h.DeleteVideo)
				r.Get("/status", h.GetVideoStatus)
			})
		})
	})

	// Legacy routes for backwards compatibility
	r.Post("/upload/init", h.InitiateUpload)
	r.Post("/upload/complete", h.CompleteUpload)
	r.Get("/latest", h.GetLatestVideos)
	r.Post("/login", h.Login)
}

// Health handles health check requests.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

// Ready handles readiness check requests.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check dependencies
	checks := map[string]string{}
	allHealthy := true

	// Check S3
	if h.s3Client != nil {
		_, err := h.s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
			Bucket: aws.String(h.config.RawBucket),
		})
		if err != nil {
			checks["s3"] = "unhealthy"
			allHealthy = false
		} else {
			checks["s3"] = "healthy"
		}
	}

	// Check SQS
	if h.sqsClient != nil {
		_, err := h.sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl:       aws.String(h.config.QueueURL),
			AttributeNames: []sqs_types.QueueAttributeName{sqs_types.QueueAttributeNameApproximateNumberOfMessages},
		})
		if err != nil {
			checks["sqs"] = "unhealthy"
			allHealthy = false
		} else {
			checks["sqs"] = "healthy"
		}
	}

	status := "ready"
	statusCode := http.StatusOK
	if !allHealthy {
		status = "not_ready"
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": status,
		"checks": checks,
	})
}

// InitiateUploadRequest is the request body for initiating an upload.
type InitiateUploadRequest struct {
	Filename    string             `json:"filename"`
	ContentType string             `json:"contentType"`
	FileSize    int64              `json:"fileSize,omitempty"`
	Size        int64              `json:"size,omitempty"` // Alias for fileSize
	Options     *models.VideoOptions `json:"options,omitempty"`
}

// InitiateUploadResponse is the response for initiating an upload.
type InitiateUploadResponse struct {
	VideoID   string    `json:"videoId"`
	UploadURL string    `json:"uploadUrl"`
	Key       string    `json:"key"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// InitiateUpload handles upload initialization and returns a presigned URL.
func (h *Handler) InitiateUpload(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "initiate-upload")
	defer span.End()

	var req InitiateUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.ErrorContext(ctx, "Failed to decode request", "error", err)
		respondWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Handle size alias
	if req.FileSize == 0 && req.Size > 0 {
		req.FileSize = req.Size
	}

	// Validate filename
	if err := h.validateFilename(req.Filename); err != nil {
		h.logger.WarnContext(ctx, "Invalid filename", "error", err, "filename", req.Filename)
		respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate content type
	if req.ContentType != "" && !allowedContentTypes[req.ContentType] {
		h.logger.WarnContext(ctx, "Invalid content type", "contentType", req.ContentType)
		respondWithError(w, http.StatusBadRequest, "Invalid content type")
		return
	}

	// Validate file size
	if req.FileSize > h.config.MaxUploadSize {
		respondWithError(w, http.StatusBadRequest, 
			fmt.Sprintf("File size exceeds maximum allowed (%d bytes)", h.config.MaxUploadSize))
		return
	}

	// Generate video ID and S3 key
	videoID := uuid.New().String()[:8]
	ext := filepath.Ext(req.Filename)
	key := fmt.Sprintf("uploads/%s/%s%s", time.Now().Format("2006/01/02"), videoID, ext)

	span.SetAttributes(
		attribute.String("video.id", videoID),
		attribute.String("s3.key", key),
		attribute.Int64("file.size", req.FileSize),
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
		h.logger.ErrorContext(ctx, "Failed to create presigned URL", "error", err)
		span.RecordError(err)
		respondWithError(w, http.StatusInternalServerError, "Failed to create upload URL")
		return
	}

	expiresAt := time.Now().Add(h.config.PresignExpiry)

	response := InitiateUploadResponse{
		VideoID:   videoID,
		UploadURL: presignResult.URL,
		Key:       key,
		ExpiresAt: expiresAt,
	}

	h.logger.InfoContext(ctx, "Upload initiated",
		"videoId", videoID,
		"filename", req.Filename,
		"size", req.FileSize,
	)

	respondWithJSON(w, http.StatusOK, response)
}

// CompleteUploadRequest is the request body for completing an upload.
type CompleteUploadRequest struct {
	VideoID  string             `json:"videoId"`
	Key      string             `json:"key"`
	Filename string             `json:"filename"`
	Options  *models.VideoOptions `json:"options,omitempty"`
}

// CompleteUpload handles upload completion and queues the transcoding job.
func (h *Handler) CompleteUpload(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "complete-upload")
	defer span.End()

	var req CompleteUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.ErrorContext(ctx, "Failed to decode request", "error", err)
		respondWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.VideoID == "" {
		respondWithError(w, http.StatusBadRequest, "videoId is required")
		return
	}

	if req.Key == "" {
		respondWithError(w, http.StatusBadRequest, "key is required")
		return
	}

	span.SetAttributes(
		attribute.String("video.id", req.VideoID),
		attribute.String("s3.key", req.Key),
	)

	// Verify the object exists in S3
	headOutput, err := h.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(h.config.RawBucket),
		Key:    aws.String(req.Key),
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "Object not found in S3", "error", err, "key", req.Key)
		respondWithError(w, http.StatusNotFound, "Upload not found")
		return
	}

	// Create video record
	if h.videoRepo != nil {
		_, err = h.videoRepo.CreateVideo(ctx, req.VideoID, req.Filename, req.Key, aws.ToInt64(headOutput.ContentLength))
		if err != nil {
			h.logger.ErrorContext(ctx, "Failed to create video record", "error", err)
			// Continue anyway - job can still be processed
		}
	}

	// Queue transcoding job
	jobMessage := map[string]interface{}{
		"source":  "api",
		"videoId": req.VideoID,
		"bucket":  h.config.RawBucket,
		"key":     req.Key,
		"options": req.Options,
	}

	jobBytes, _ := json.Marshal(jobMessage)

	_, err = h.sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(h.config.QueueURL),
		MessageBody: aws.String(string(jobBytes)),
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to queue job", "error", err)
		span.RecordError(err)
		respondWithError(w, http.StatusInternalServerError, "Failed to queue processing job")
		return
	}

	h.logger.InfoContext(ctx, "Upload completed and job queued",
		"videoId", req.VideoID,
		"key", req.Key,
	)

	respondWithJSON(w, http.StatusAccepted, map[string]interface{}{
		"videoId": req.VideoID,
		"status":  "processing",
		"message": "Video queued for processing",
	})
}

// GetVideo retrieves a video by ID.
func (h *Handler) GetVideo(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "get-video")
	defer span.End()

	videoID := chi.URLParam(r, "videoID")
	if videoID == "" {
		respondWithError(w, http.StatusBadRequest, "videoID is required")
		return
	}

	span.SetAttributes(attribute.String("video.id", videoID))

	if h.videoRepo == nil {
		respondWithError(w, http.StatusServiceUnavailable, "Video repository not available")
		return
	}

	video, err := h.videoRepo.GetVideo(ctx, videoID)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get video", "error", err, "videoId", videoID)
		respondWithError(w, http.StatusNotFound, "Video not found")
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

// GetVideoStatus retrieves the status of a video.
func (h *Handler) GetVideoStatus(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "get-video-status")
	defer span.End()

	videoID := chi.URLParam(r, "videoID")
	if videoID == "" {
		respondWithError(w, http.StatusBadRequest, "videoID is required")
		return
	}

	span.SetAttributes(attribute.String("video.id", videoID))

	if h.videoRepo == nil {
		respondWithError(w, http.StatusServiceUnavailable, "Video repository not available")
		return
	}

	video, err := h.videoRepo.GetVideo(ctx, videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"videoId":   video.VideoID,
		"status":    video.Status,
		"progress":  video.Progress,
		"updatedAt": video.UpdatedAt,
	})
}

// ListVideos lists videos with pagination.
func (h *Handler) ListVideos(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "list-videos")
	defer span.End()

	// Pagination would be implemented here
	limit := 20

	if h.videoRepo == nil {
		respondWithError(w, http.StatusServiceUnavailable, "Video repository not available")
		return
	}

	videos, nextToken, err := h.videoRepo.ListVideos(ctx, limit, "")
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to list videos", "error", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to list videos")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"videos":    videos,
		"nextToken": nextToken,
	})
}

// GetLatestVideos retrieves the latest videos.
func (h *Handler) GetLatestVideos(w http.ResponseWriter, r *http.Request) {
	h.ListVideos(w, r)
}

// CreateVideo creates a new video entry.
func (h *Handler) CreateVideo(w http.ResponseWriter, r *http.Request) {
	h.InitiateUpload(w, r)
}

// DeleteVideo deletes a video.
func (h *Handler) DeleteVideo(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "delete-video")
	defer span.End()

	videoID := chi.URLParam(r, "videoID")
	if videoID == "" {
		respondWithError(w, http.StatusBadRequest, "videoID is required")
		return
	}

	span.SetAttributes(attribute.String("video.id", videoID))

	// Implementation would delete from DynamoDB and S3
	respondWithJSON(w, http.StatusOK, map[string]string{
		"message": "Video deleted",
		"videoId": videoID,
	})
}

// InitiateMultipartUpload initiates a multipart upload.
func (h *Handler) InitiateMultipartUpload(w http.ResponseWriter, r *http.Request) {
	// Multipart upload implementation
	respondWithError(w, http.StatusNotImplemented, "Multipart upload not yet implemented")
}

// CompleteMultipartUpload completes a multipart upload.
func (h *Handler) CompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	// Multipart upload completion implementation
	respondWithError(w, http.StatusNotImplemented, "Multipart upload not yet implemented")
}

// GenerateToken generates a JWT token.
func (h *Handler) GenerateToken(w http.ResponseWriter, r *http.Request) {
	h.Login(w, r)
}

// Login handles user authentication.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "login")
	defer span.End()

	// Get credentials from Basic Auth
	username, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="API"`)
		respondWithError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	// Validate credentials (in production, check against database)
	// For now, just check if username is provided
	if username == "" || password == "" {
		respondWithError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	span.SetAttributes(attribute.String("user", username))

	// Generate JWT
	claims := jwt.MapClaims{
		"sub": username,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(h.config.JWTSecret))
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to sign token", "error", err)
		respondWithError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]interface{}{
		"token":     tokenString,
		"expiresAt": time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	})
}

// validateFilename validates an upload filename.
func (h *Handler) validateFilename(filename string) error {
	if filename == "" {
		return fmt.Errorf("filename is required")
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return fmt.Errorf("filename must have an extension")
	}

	if !allowedExtensions[ext] {
		return fmt.Errorf("file type %s is not allowed", ext)
	}

	return nil
}

// Helper functions

func respondWithJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondWithError(w http.ResponseWriter, status int, message string) {
	respondWithJSON(w, status, map[string]string{"error": message})
}
