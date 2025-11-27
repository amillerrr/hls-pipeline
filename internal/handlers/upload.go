package handlers

import (
	"crypto/subtle"
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
)

var tracer = otel.Tracer("eye-api")

const maxUploadSize = 500 << 20 // 500 MB

type API struct {
	s3Client  *s3.Client
	sqsClient *sqs.Client
	sqsQueueURL  string
	log    *slog.Logger
}

func New(s3 *s3.Client, sqsClient *sqs.Client, sqsQueueURL string, log *slog.Logger) *API {
	return &API{
		s3Client:  s3,
		sqsClient: sqsClient,
		sqsQueueURL:  sqsQueueURL,
		log:    log,
	}
}

// Handle user authentication and return JWT token
func (a *API) LoginHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

	usernameMatch := subtle.ConstantTimeCompare([]byte(username), []byte(expectedUsername)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(password), []byte(expectedPassword)) == 1

	if !usernameMatch || !passwordMatch {
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
	json.NewEncoder(w).Encode(map[string]string{
		"token": token,
	})
}

func (a *API) UploadHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

		// Handle CORS preflight
	if r.Method == http.MethodOptions {
		a.handleCORSPreflight(w, r)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, span := tracer.Start(ctx, "upload-handler",
		trace.WithAttributes(attribute.String("handler", "upload")))
	defer span.End()

	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		logger.Error(ctx, a.log, "Failed to parse multipart form", "error", err)
		http.Error(w, "File too large or invalid form", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("video")
	if err != nil {
		logger.Error(ctx, a.log, "Failed to get form file", "error", err)
		http.Error(w, "Failed to get uploaded file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate file extension
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]bool{
		".mp4":  true,
		".mov":  true,
		".avi":  true,
		".mkv":  true,
		".webm": true,
	}
	if !allowedExts[ext] {
		http.Error(w, "Invalid file type. Allowed: mp4, mov, avi, mkv, webm", http.StatusBadRequest)
		return
	}

	// Validate content type
	contentType := header.Header.Get("Content-Type")
	allowedTypes := map[string]bool{
		"video/mp4":       true,
		"video/quicktime": true,
		"video/x-msvideo": true,
		"video/x-matroska": true,
		"video/webm":      true,
	}
	if contentType != "" && !allowedTypes[contentType] {
		// Read first 512 bytes 
		buf := make([]byte, 512)
		n, _ := file.Read(buf)
		detectedType := http.DetectContentType(buf[:n])
		file.Seek(0, 0) // Reset file pointer

		if !strings.HasPrefix(detectedType, "video/") {
			http.Error(w, "Invalid content type", http.StatusBadRequest)
			return
		}
	}

	// Generate unique key
	videoID := uuid.New().String()
	s3Key := fmt.Sprintf("uploads/%s%s", videoID, ext)

	span.SetAttributes(
		attribute.String("video.id", videoID),
		attribute.String("video.filename", header.Filename),
		attribute.Int64("video.size", header.Size),
	)

	// Upload to S3 using the request context
	bucket := os.Getenv("S3_BUCKET")
	_, err = a.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(s3Key),
		Body:          file,
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(header.Size),
	})
	if err != nil {
		logger.Error(ctx, a.log, "Failed to upload to S3", "error", err, "key", s3Key)
		http.Error(w, "Failed to upload file", http.StatusInternalServerError)
		return
	}

	logger.Info(ctx, a.log, "File uploaded to S3",
		"key", s3Key,
		"size", header.Size,
		"videoId", videoID,
	)

	// Queue processing job 
	message := map[string]string{
		"videoId":  videoID,
		"s3Key":    s3Key,
		"bucket":   bucket,
		"filename": header.Filename,
	}
	messageBytes, _ := json.Marshal(message)

	_, err = a.sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(a.sqsQueueURL),
		MessageBody: aws.String(string(messageBytes)),
	})
	if err != nil {
		logger.Error(ctx, a.log, "Failed to queue processing job", "error", err, "videoId", videoID)
	}

	logger.Info(ctx, a.log, "Processing job queued", "videoId", videoID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"videoId": videoID,
		"status":  "processing",
		"message": "Video uploaded successfully and queued for processing",
	})
}

// Return the most recently processed video
func (a *API) GetLatestVideoHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Handle CORS preflight
	if r.Method == http.MethodOptions {
		a.handleCORSPreflight(w, r)
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
	json.NewEncoder(w).Encode(map[string]interface{}{
		"videoId":     videoID,
		"playbackUrl": playbackURL,
		"processedAt": latestTime.Format(time.RFC3339),
	})
}

// handle requests for CORS
func (a *API) handleCORSPreflight(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "*"
	}

	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Max-Age", "86400") // 24 hours
	w.WriteHeader(http.StatusNoContent)
}
