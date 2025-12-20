package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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
	"github.com/amillerrr/hls-pipeline/internal/config"
	"github.com/amillerrr/hls-pipeline/internal/storage"
	"github.com/amillerrr/hls-pipeline/pkg/models"
)

var tracer = otel.Tracer("hls-api")

// Configuration constants
const (
	PresignedURLExpiration = 10 * time.Minute
	MaxFilenameLength      = 255
	MaxListObjects         = 1000
	MaxRequestBodySize     = 1 << 20 // 1 MB
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

// Handlers contains all HTTP handlers for the API.
type Handlers struct {
	cfg        *config.Config
	log        *slog.Logger
	s3Client   *storage.S3Client
	sqsClient  *sqs.Client
	videoRepo  *storage.VideoRepository
	jwtService *auth.JWTService
}

// HandlersConfig holds dependencies for handlers.
type HandlersConfig struct {
	Config     *config.Config
	Logger     *slog.Logger
	S3Client   *storage.S3Client
	SQSClient  *sqs.Client
	VideoRepo  *storage.VideoRepository
	JWTService *auth.JWTService
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(cfg *HandlersConfig) *Handlers {
	return &Handlers{
		cfg:        cfg.Config,
		log:        cfg.Logger,
		s3Client:   cfg.S3Client,
		sqsClient:  cfg.SQSClient,
		videoRepo:  cfg.VideoRepo,
		jwtService: cfg.JWTService,
	}
}

// writeJSON writes a JSON response.
func (h *Handlers) writeJSON(ctx context.Context, w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.ErrorContext(ctx, "Failed to encode JSON response", "error", err)
	}
}

// writeError writes an error response.
func (h *Handlers) writeError(ctx context.Context, w http.ResponseWriter, status int, message string) {
	h.writeJSON(ctx, w, status, map[string]string{"error": message})
}

// limitRequestBody wraps the request body with a size limit.
func (h *Handlers) limitRequestBody(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodySize)
}

// LoginHandler handles user authentication and returns a JWT token.
func (h *Handlers) LoginHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientIP := auth.GetClientIP(r)

	username, password, ok := r.BasicAuth()
	if !ok {
		h.writeError(ctx, w, http.StatusUnauthorized, "Missing credentials")
		return
	}

	expectedUsername, expectedPassword, err := h.cfg.GetAPICredentials()
	if err != nil {
		h.log.ErrorContext(ctx, "Failed to get API credentials", "error", err)
		h.writeError(ctx, w, http.StatusInternalServerError, "Server configuration error")
		return
	}

	if username != expectedUsername || password != expectedPassword {
		h.log.WarnContext(ctx, "Failed login attempt", "username", username, "ip", clientIP)
		h.writeError(ctx, w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	token, err := h.jwtService.GenerateToken(username)
	if err != nil {
		h.log.ErrorContext(ctx, "Failed to generate token", "error", err)
		h.writeError(ctx, w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	h.log.InfoContext(ctx, "Successful login", "username", username, "ip", clientIP)
	h.writeJSON(ctx, w, http.StatusOK, map[string]string{"token": token})
}

// InitUploadRequest is the request payload for upload initialization.
type InitUploadRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
}

// InitUploadResponse is the response payload for upload initialization.
type InitUploadResponse struct {
	UploadURL string `json:"uploadUrl"`
	VideoID   string `json:"videoId"`
	Key       string `json:"key"`
	RequestID string `json:"requestId"`
}

// InitUploadHandler generates a presigned URL for video upload.
func (h *Handlers) InitUploadHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		h.writeError(ctx, w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	requestID := uuid.New().String()
	ctx, span := tracer.Start(ctx, "init-upload-handler",
		trace.WithAttributes(
			attribute.String("handler", "init-upload"),
			attribute.String("request.id", requestID),
		))
	defer span.End()

	h.limitRequestBody(w, r)

	var req InitUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		span.RecordError(err)
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			h.writeError(ctx, w, http.StatusRequestEntityTooLarge, "Request body too large")
			return
		}
		h.writeError(ctx, w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate filename
	if err := validateFilename(req.Filename); err != nil {
		span.RecordError(err)
		h.writeError(ctx, w, http.StatusBadRequest, err.Error())
		return
	}

	// Validate content type
	if err := validateContentType(req.ContentType); err != nil {
		span.RecordError(err)
		h.writeError(ctx, w, http.StatusBadRequest, err.Error())
		return
	}

	// Generate unique key
	videoID := uuid.New().String()
	ext := strings.ToLower(filepath.Ext(req.Filename))
	s3Key := fmt.Sprintf("uploads/%s%s", videoID, ext)

	span.SetAttributes(
		attribute.String("video.id", videoID),
		attribute.String("video.key", s3Key),
		attribute.String("video.content_type", req.ContentType),
	)

	// Generate presigned URL
	presignedURL, err := h.s3Client.GeneratePresignedURL(ctx, h.cfg.AWS.RawBucket, s3Key, req.ContentType, PresignedURLExpiration)
	if err != nil {
		span.RecordError(err)
		h.log.ErrorContext(ctx, "Failed to generate presigned URL",
			"error", err,
			"videoId", videoID,
			"requestId", requestID,
		)
		h.writeError(ctx, w, http.StatusInternalServerError, "Internal server error")
		return
	}

	h.log.InfoContext(ctx, "Generated presigned URL",
		"videoId", videoID,
		"key", s3Key,
		"filename", req.Filename,
		"requestId", requestID,
	)

	h.writeJSON(ctx, w, http.StatusOK, InitUploadResponse{
		UploadURL: presignedURL,
		VideoID:   videoID,
		Key:       s3Key,
		RequestID: requestID,
	})
}

// CompleteUploadRequest is the request payload for completing an upload.
type CompleteUploadRequest struct {
	VideoID  string `json:"videoId"`
	Key      string `json:"key"`
	Filename string `json:"filename"`
}

// CompleteUploadResponse is the response payload for completed uploads.
type CompleteUploadResponse struct {
	VideoID   string `json:"videoId"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

// CompleteUploadHandler verifies the upload and queues the processing job.
func (h *Handlers) CompleteUploadHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		h.writeError(ctx, w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	requestID := uuid.New().String()
	ctx, span := tracer.Start(ctx, "complete-upload-handler",
		trace.WithAttributes(
			attribute.String("handler", "complete-upload"),
			attribute.String("request.id", requestID),
		))
	defer span.End()

	h.limitRequestBody(w, r)

	var req CompleteUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		span.RecordError(err)
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			h.writeError(ctx, w, http.StatusRequestEntityTooLarge, "Request body too large")
			return
		}
		h.writeError(ctx, w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate required fields
	if req.VideoID == "" {
		h.writeError(ctx, w, http.StatusBadRequest, "videoId is required")
		return
	}
	if req.Key == "" {
		h.writeError(ctx, w, http.StatusBadRequest, "key is required")
		return
	}

	// Validate S3 key format
	if err := validateS3Key(req.Key, req.VideoID); err != nil {
		span.RecordError(err)
		h.log.WarnContext(ctx, "Invalid S3 key format",
			"key", req.Key,
			"videoId", req.VideoID,
			"requestId", requestID,
			"error", err,
		)
		h.writeError(ctx, w, http.StatusBadRequest, err.Error())
		return
	}

	span.SetAttributes(
		attribute.String("video.id", req.VideoID),
		attribute.String("video.key", req.Key),
	)

	// Verify file exists in S3
	headResult, err := h.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(h.cfg.AWS.RawBucket),
		Key:    aws.String(req.Key),
	})
	if err != nil {
		span.RecordError(err)
		h.log.WarnContext(ctx, "File not found in S3",
			"key", req.Key,
			"videoId", req.VideoID,
			"requestId", requestID,
			"error", err,
		)
		h.writeError(ctx, w, http.StatusNotFound, "Video file not found in S3")
		return
	}

	var fileSizeBytes int64
	if headResult.ContentLength != nil {
		fileSizeBytes = *headResult.ContentLength
		span.SetAttributes(attribute.Int64("video.size_bytes", fileSizeBytes))
	}

	// Create video record in DynamoDB
	if h.videoRepo != nil {
		_, err := h.videoRepo.CreateVideo(ctx, req.VideoID, req.Filename, req.Key, fileSizeBytes)
		if err != nil {
			h.log.WarnContext(ctx, "Failed to create video record in DynamoDB",
				"videoId", req.VideoID,
				"error", err,
				"requestId", requestID,
			)
		}
	}

	// Queue processing job
	message := map[string]string{
		"videoId":  req.VideoID,
		"s3Key":    req.Key,
		"bucket":   h.cfg.AWS.RawBucket,
		"filename": req.Filename,
	}

	messageBytes, err := json.Marshal(message)
	if err != nil {
		span.RecordError(err)
		h.log.ErrorContext(ctx, "Failed to marshal message",
			"error", err,
			"videoId", req.VideoID,
			"requestId", requestID,
		)
		h.writeError(ctx, w, http.StatusInternalServerError, "Internal server error")
		return
	}

	_, err = h.sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(h.cfg.AWS.SQSQueueURL),
		MessageBody: aws.String(string(messageBytes)),
	})
	if err != nil {
		span.RecordError(err)
		h.log.ErrorContext(ctx, "Failed to queue processing job",
			"error", err,
			"videoId", req.VideoID,
			"requestId", requestID,
		)
		h.writeError(ctx, w, http.StatusInternalServerError, "Failed to queue job")
		return
	}

	h.log.InfoContext(ctx, "Processing job queued",
		"videoId", req.VideoID,
		"requestId", requestID,
	)

	h.writeJSON(ctx, w, http.StatusAccepted, CompleteUploadResponse{
		VideoID:   req.VideoID,
		Status:    "processing",
		Message:   "Video queued for processing",
		RequestID: requestID,
	})
}

// LatestVideoResponse is the response payload for the latest video endpoint.
type LatestVideoResponse struct {
	VideoID     string `json:"videoId"`
	PlaybackURL string `json:"playbackUrl"`
	ProcessedAt string `json:"processedAt"`
}

// GetLatestVideoHandler returns the most recently processed video.
func (h *Handlers) GetLatestVideoHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodGet {
		h.writeError(ctx, w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx, span := tracer.Start(ctx, "get-latest-video")
	defer span.End()

	if h.videoRepo != nil {
		video, err := h.videoRepo.GetLatestVideo(ctx)
		if err != nil {
			if errors.Is(err, models.ErrVideoNotFound) {
				h.writeError(ctx, w, http.StatusNotFound, "No processed videos found")
				return
			}
			span.RecordError(err)
			h.log.ErrorContext(ctx, "Failed to get latest video from DynamoDB", "error", err)
			h.writeError(ctx, w, http.StatusInternalServerError, "Failed to retrieve video")
			return
		}

		span.SetAttributes(
			attribute.String("video.id", video.VideoID),
		)

		h.writeJSON(ctx, w, http.StatusOK, LatestVideoResponse{
			VideoID:     video.VideoID,
			PlaybackURL: video.PlaybackURL,
			ProcessedAt: video.ProcessedAt,
		})
		return
	}

	h.writeError(ctx, w, http.StatusNotFound, "No processed videos found")
}

// Validation functions

func validateFilename(filename string) error {
	if filename == "" {
		return errors.New("filename is required")
	}
	if len(filename) > MaxFilenameLength {
		return models.ErrFilenameTooLong
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if !AllowedExtensions[ext] {
		return fmt.Errorf("%w: allowed extensions are mp4, mov, avi, mkv, webm", models.ErrInvalidFileType)
	}

	return nil
}

func validateContentType(contentType string) error {
	if contentType == "" {
		return errors.New("content type is required")
	}
	if !AllowedContentTypes[contentType] {
		return fmt.Errorf("%w: %s", models.ErrInvalidContentType, contentType)
	}
	return nil
}

func validateS3Key(key, videoID string) error {
	decodedKey, err := url.PathUnescape(key)
	if err != nil {
		return fmt.Errorf("%w: invalid URL encoding", models.ErrInvalidKeyFormat)
	}

	if strings.Contains(decodedKey, "..") || strings.Contains(key, "..") {
		return fmt.Errorf("%w: path traversal not allowed", models.ErrInvalidKeyFormat)
	}

	expectedPrefix := fmt.Sprintf("uploads/%s", videoID)
	if !strings.HasPrefix(key, expectedPrefix) {
		return fmt.Errorf("%w: key must start with %s", models.ErrInvalidKeyFormat, expectedPrefix)
	}

	ext := strings.ToLower(filepath.Ext(key))
	if !AllowedExtensions[ext] {
		return fmt.Errorf("%w: invalid extension in key", models.ErrInvalidKeyFormat)
	}

	return nil
}
