package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/amillerrr/hls-pipeline/internal/observability"
	"github.com/amillerrr/hls-pipeline/internal/storage"
)

type Job struct {
	FileID string `json:"file_id"`
}

// Metrics
var (
		transcodeDuration = promauto.NewHistogram(prometheus.HistogramOpts{
			Name:    "transcode_duration_seconds",
			Help:    "Time taken to transcode video",
			Buckets: prometheus.LinearBuckets(10, 10, 10),
		})
		activeJobs = promauto.NewGauge(prometheus.GaugeOpts{
			Name: "worker_active_jobs",
			Help: "Number of jobs currently processing on this node",
		})
)

// Semaphore token
type token struct{}

func main() {
	// Initialize Logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Load .env file
	if err := godotenv.Load(); err != nil {
		logger.Warn("No .env file found", 
			"details", "relying on system ENV variables",
			"error", err.Error(),
		)
	} else {
		logger.Info("Environment variables loaded from .env")
	}

	// Inititialize Distributed Tracing
	shutdown := observability.InitTracer(context.Background(), "eye-worker")
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			logger.Error("Failed to shutdown tracer", "error", err)
		}
	}()

	// AWS Config and SQS Initialization
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		logger.Error("Failed to load AWS config", "error", err)
		os.Exit(1)
	}
	otelaws.AppendMiddlewares(&cfg.APIOptions)
	sqsClient := sqs.NewFromConfig(cfg)
	queueURL := os.Getenv("SQS_QUEUE_URL")
	if queueURL == "" {
		logger.Error("SQS_QUEUE_URL is not set")
		os.Exit(1)
	}

	// Initialize S3
	s3Client, err := storage.NewS3Client()
	if err != nil {
		logger.Error("Failed to init S3", "error", err)
		os.Exit(1)
	}

	// Metrics Server
	go func() {
		metricsPort := ":2112"
		logger.Info("Starting Metrics Server", "port", metricsPort)
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(metricsPort, nil); err != nil {
			logger.Error("Metrics server failed", "error", err)
		}
	}()

	// Concurrency Limiter
	numCores := runtime.NumCPU()
	maxConcurrency := numCores - 1
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	
	logger.Info("Worker started", "cores", numCores, "max_jobs", maxConcurrency, "mode", "ABR_SQS_OTel")

	sem := make(chan token, maxConcurrency)

	for {
		// Long Polling SQS
		msgOutput, err := sqsClient.ReceiveMessage(context.TODO(), &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 1,
			WaitTimeSeconds:     20,
			VisibilityTimeout:   600,
			MessageAttributeNames: []string{"All"},
		})
		
		if err != nil {
			logger.Error("SQS receive failed", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if len(msgOutput.Messages) == 0 {
			continue
		}

		msg := msgOutput.Messages[0]

		// Acquire Semaphore
		sem <- token{}
		activeJobs.Inc()

		go func(m types.Message) {
			defer func() { 
				<-sem 
				activeJobs.Dec()
			}()

			carrier := propagation.MapCarrier{}
			for k, v := range m.MessageAttributes {
				if v.StringValue != nil {
					carrier[k] = *v.StringValue
				}
			}

			parentCtx := otel.GetTextMapPropagator().Extract(context.Background(), carrier)

			// Start new Worker Span
			tracer := otel.Tracer("worker")
			ctx, span := tracer.Start(parentCtx, "process_job",
				trace.WithAttributes(attribute.String("sqs.message_id", *m.MessageId)))
			defer span.End()

			// Parse Job
			var job Job
			if err := json.Unmarshal([]byte(*m.Body), &job); err != nil {
				logger.Error("Invalid job format", "body", *m.Body)
				deleteMessage(sqsClient, queueURL, m.ReceiptHandle, logger) 
				return
			}

			span.SetAttributes(attribute.String("job.file_id", job.FileID))
			logger.Info("Processing job", "job_id", job.FileID, "trace_id", span.SpanContext().TraceID())

			// Transcode 
			if err := processVideoABR(ctx, s3Client, job, logger); err != nil {
				logger.Error("Job failed", "job_id", job.FileID, "error", err)
			} else {
				logger.Info("Job complete", "job_id", job.FileID)
				deleteMessage(sqsClient, queueURL, m.ReceiptHandle, logger)
			}
		}(msg)
	}
}

func deleteMessage(client *sqs.Client, queueURL string, receiptHandle *string, logger *slog.Logger) {
	_, err := client.DeleteMessage(context.TODO(), &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueURL),
		ReceiptHandle: receiptHandle,
	})
	if err != nil {
		logger.Error("Failed to delete SQS message", "error", err)
	}
}

func processVideoABR(ctx context.Context, s3Client *s3.Client, job Job, logger *slog.Logger) error {
	start := time.Now()

	tracer := otel.Tracer("worker")
	ctx, span := tracer.Start(ctx, "transcode_abr")
	defer span.End()

	// Increase timeout for ABR transcoding
	procCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	// Prepare Paths
	tempInput := filepath.Join("/tmp/uploads", job.FileID)
	outputDir := filepath.Join("/tmp/hls", job.FileID)
	
	os.MkdirAll("/tmp/uploads", 0755)
	os.MkdirAll(outputDir, 0755)
	
	defer os.Remove(tempInput)
	defer os.RemoveAll(outputDir) 

	// Download from S3
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		return fmt.Errorf("S3_BUCKET env var not set")
	}

	logger.Info("Downloading raw video...", "key", "uploads/"+job.FileID)
	destFile, err := os.Create(tempInput)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer destFile.Close() 

	resp, err := s3Client.GetObject(procCtx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String("uploads/" + job.FileID),
	})
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if _, err := io.Copy(destFile, resp.Body); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// ABR Transcode
	// Variants: 1080p, 720p, 480p
	filterComplex := "[0:v]split=3[v1][v2][v3];" +
		"[v1]scale=w=1920:h=1080[v1out];" +
		"[v2]scale=w=1280:h=720[v2out];" +
		"[v3]scale=w=854:h=480[v3out]"

	cmd := exec.CommandContext(procCtx, "ffmpeg",
		"-i", tempInput,
		"-filter_complex", filterComplex,
		
		// Stream 1: 1080p (High)
		"-map", "[v1out]", "-c:v:0", "libx264", "-b:v:0", "4500k", "-maxrate:v:0", "5000k", "-bufsize:v:0", "7500k",
		
		// Stream 2: 720p (Med)
		"-map", "[v2out]", "-c:v:1", "libx264", "-b:v:1", "2500k", "-maxrate:v:1", "2750k", "-bufsize:v:1", "3750k",
		
		// Stream 3: 480p (Low)
		"-map", "[v3out]", "-c:v:2", "libx264", "-b:v:2", "1000k", "-maxrate:v:2", "1100k", "-bufsize:v:2", "1500k",
		
		// Audio (Copied to all streams)
		// "-map", "a:0", "-c:a", "aac", "-b:a", "128k", "-ac", "2",
		
		// HLS Settings
		"-f", "hls",
		// "-var_stream_map", "v:0,a:0 v:1,a:0 v:2,a:0",
		"-var_stream_map", "v:0 v:1 v:2",
		"-master_pl_name", "master.m3u8",
		"-hls_time", "4",
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(outputDir, "%v", "segment_%03d.ts"),
		filepath.Join(outputDir, "%v", "playlist.m3u8"),
	)

	// FFmpeg writes to stderr for logging
	cmd.Stderr = os.Stderr 

	logger.Info("Starting ABR transcoding...")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	logger.Info("Transcoding complete. Uploading to S3...")

	// Upload Recursive Directory
	processedBucket := os.Getenv("PROCESSED_BUCKET")
	if processedBucket == "" {
		return fmt.Errorf("PROCESSED_BUCKET env var not set")
	}

	err = uploadDirectoryToS3(procCtx, s3Client, outputDir, processedBucket, job.FileID)
	if err != nil {
		return fmt.Errorf("failed to upload HLS: %w", err)
	}

	duration := time.Since(start).Seconds()
	transcodeDuration.Observe(duration)

	return nil
}

func uploadDirectoryToS3(ctx context.Context, s3Client *s3.Client, localDir, bucket, s3Prefix string) error {
	return filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}

		// Create S3 Key
		key := filepath.ToSlash(filepath.Join(s3Prefix, relPath))

		// Open file
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		// Set Content-Type
		contentType := "application/octet-stream"
		switch filepath.Ext(path) {
		case ".m3u8":
			contentType = "application/vnd.apple.mpegurl"
		case ".ts":
			contentType = "video/mp2t"
		}

		// Upload
		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			Body:        file,
			ContentType: aws.String(contentType),
		})
		return err
	})
}
