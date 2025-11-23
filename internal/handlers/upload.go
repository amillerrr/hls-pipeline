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
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/amillerrr/hls-pipeline/internal/auth"
)

var (
	uploadOps = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "video_upload_total",
			Help: "The total number of processed uploaded videos",
		},
		[]string{"status"},
	)
)

// APIHandler
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
	user := r.FormValue("username")
	pass := r.FormValue("password")

	if user != "admin" || pass != "secret" {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := auth.GenerateToken(user)
	if err != nil {
		http.Error(w, "Signing Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(fmt.Sprintf(`{"token": "%s"}`, token)))
}

func (h *APIHandler) UploadHandler(w http.ResponseWriter, r *http.Request) {
	// Start Tracing Span
	ctx := r.Context()
	tracer := otel.Tracer("api-handler")
	ctx, span := tracer.Start(ctx, "process_upload_request")
	defer span.End()

	start := time.Now()
	requestID := uuid.New().String()

	logger := h.Logger.With(
		slog.String("req_id", requestID),
		slog.String("method", r.Method),
	)

	// Validation Checks
	if r.Method != http.MethodPost {
		uploadOps.WithLabelValues("error_method").Inc()
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadSize)
	if err := r.ParseMultipartForm(MaxUploadSize); err != nil {
		logger.Error("File too large", "error", err)
		http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		logger.Error("Form file error", "error", err)
		http.Error(w, "Invalid file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Upload to S3
	fileUUID := uuid.New().String()
	safeFilename := fmt.Sprintf("%s%s", fileUUID, filepath.Ext(header.Filename))
	
	bucket := os.Getenv("S3_BUCKET")
	key := fmt.Sprintf("uploads/%s", safeFilename)

	_, err = h.S3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        file,
		ContentType: aws.String("video/mp4"),
	})

	if err != nil {
		logger.Error("S3 Upload Failed", "error", err)
		http.Error(w, "Storage Error", http.StatusInternalServerError)
		return
	}

	// Dispatch to SQS 
	job := map[string]string{"file_id": safeFilename}
	payload, _ := json.Marshal(job)

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
		logger.Error("SQS Dispatch Failed", "error", err)
		http.Error(w, "Queue Error", http.StatusInternalServerError)
		return
	}

	// Log success
	duration := time.Since(start)
	logger.Info("Ingest Complete", "filename", safeFilename, "duration", duration)
	uploadOps.WithLabelValues("success").Inc()

	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(fmt.Sprintf(`{"status": "processing", "id": "%s"}`, safeFilename)))
}
