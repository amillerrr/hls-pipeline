package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/amillerrr/hls-pipeline/internal/auth"
	"github.com/amillerrr/hls-pipeline/internal/logger"
)

var (
	uploadOps = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "video_upload_total",
			Help: "The total number of processed uploaded videos",
		},
		[]string{"status"},
	)
	uploadDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "video_upload_duration_seconds",
		Help:    "Time taken to process upload request",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
	})
)

type APIHandler struct {
	S3Client  *s3.Client
	SQSClient *sqs.Client
	QueueURL  string
	Logger    *slog.Logger
}

func New(s3 *s3.Client, sqs *sqs.Client, queueURL string, logger *slog.Logger) *APIHandler {
	return &APIHandler{
		S3Client:  s3,
		SQSClient: sqs,
		QueueURL:  queueURL,
		Logger:    logger,
	}
}

const MaxUploadSize = 500 << 20 // 500 MB

func (h *APIHandler) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	user := r.FormValue("username")
	pass := r.FormValue("password")

	expectedUsername := os.Getenv("API_USERNAME")
	expectedPassword := os.Getenv("API_PASSWORD")

	if expectedUsername == "" || expectedPassword == "" {
		env := os.Getenv("ENV")
		if env == "prod" || env == "production" {
			h.Logger.Error("CRITICAL: API_USERNAME or API_PASSWORD not set in production")
			http.Error(w, "Server configuration error", http.StatusInternalServerError)
			return
		}
		// Development fallback with warning
		h.Logger.Warn("Using default credentials - DO NOT USE IN PRODUCTION")
		expectedUsername = "admin"
		expectedPassword = "secret"
	}

	usernameMatch := subtle.ConstantTimeCompare([]byte(user), []byte(expectedUsername)) == 1
	passwordMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(expectedPassword)) == 1

	if !usernameMatch || !passwordMatch {
		time.Sleep(100 * time.Millisecond)
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := auth.GenerateToken(user)
	if err != nil {
		h.Logger.Error("Failed to generate token", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{
		"token": token,
	})
}

func (h *APIHandler) UploadHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tracer := otel.Tracer("api-handler")
	ctx, span := tracer.Start(ctx, "process_upload_request")
	defer span.End()

	start := time.Now()
	requestID := uuid.New().String()
	reqLogger := h.Logger.With(
		slog.String("req_id", requestID),
		slog.String("method", r.Method),
	)

	defer func() {
		uploadDuration.Observe(time.Since(start).Seconds())
	}()

	if r.Method != http.MethodPost {
		uploadOps.WithLabelValues("error_method").Inc()
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadSize)
	if err := r.ParseMultipartForm(MaxUploadSize); err != nil {
		logger.Error(ctx, reqLogger, "File too large", "error", err)
		uploadOps.WithLabelValues("error_size").Inc()
		http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		logger.Error(ctx, reqLogger, "Form file error", "error", err)
		uploadOps.WithLabelValues("error_form").Inc()
		http.Error(w, "Invalid file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate file extension
	ext := filepath.Ext(header.Filename)
	allowedExts := map[string]bool{
		".mp4":  true,
		".mov":  true,
		".avi":  true,
		".mkv":  true,
		".webm": true,
	}
	if !allowedExts[ext] {
		logger.Error(ctx, reqLogger, "Invalid file extension", "extension", ext)
		uploadOps.WithLabelValues("error_extension").Inc()
		http.Error(w, "Invalid file type. Allowed: mp4, mov, avi, mkv, webm", http.StatusBadRequest)
		return
	}

	fileUUID := uuid.New().String()
	safeFilename := fmt.Sprintf("%s%s", fileUUID, ext)
	
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		logger.Error(ctx, reqLogger, "S3_BUCKET not configured")
		http.Error(w, "Server configuration error", http.StatusInternalServerError)
		return
	}

	key := fmt.Sprintf("uploads/%s", safeFilename)

	_, err = h.S3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        file,
		ContentType: aws.String(getMimeType(ext)),
	})

	if err != nil {
		logger.Error(ctx, reqLogger, "S3 Upload Failed", "error", err)
		uploadOps.WithLabelValues("error_s3").Inc()
		http.Error(w, "Storage Error", http.StatusInternalServerError)
		return
	}

	job := map[string]string{"file_id": safeFilename}
	payload, err := json.Marshal(job)
	if err != nil {
		logger.Error(ctx, reqLogger, "Failed to marshal job", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	// Propagate trace context to SQS
	msgAttrs := make(map[string]types.MessageAttributeValue)
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	for k, v := range carrier {
		msgAttrs[k] = types.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String(v),
		}
	}

	_, err = h.SQSClient.SendMessage(context.TODO(), &sqs.SendMessageInput{
		QueueUrl:    aws.String(h.QueueURL),
		MessageBody: aws.String(string(payload)),
		MessageAttributes: msgAttrs,
	})

	if err != nil {
		logger.Error(ctx, reqLogger, "SQS Dispatch Failed", "error", err)
		uploadOps.WithLabelValues("error_sqs").Inc()
		http.Error(w, "Queue Error", http.StatusInternalServerError)
		return
	}

	duration := time.Since(start)
	logger.Info(ctx, reqLogger, "Ingest Complete", 
		"filename", safeFilename,
		"size_bytes", header.Size,
		"duration", duration,
	)
	uploadOps.WithLabelValues("success").Inc()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "processing",
		"id":     safeFilename,
	})
}

// Return MIME type for video
func getMimeType(ext string) string {
	mimeTypes := map[string]string{
		".mp4":  "video/mp4",
		".mov":  "video/quicktime",
		".avi":  "video/x-msvideo",
		".mkv":  "video/x-matroska",
		".webm": "video/webm",
	}
	if mime, ok := mimeTypes[ext]; ok {
		return mime
	}
	return "application/octet-stream"
}
