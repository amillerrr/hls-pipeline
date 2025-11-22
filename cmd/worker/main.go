package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

	"github.com/amillerrr/hls-pipeline/internal/storage"
)

type Job struct {
	FileID string `json:"file_id"`
}

// Metric: How long does transcoding take?
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
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, relying on system ENV variables")
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 1. AWS Config & SQS Init
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		logger.Error("Failed to load AWS config", "error", err)
		os.Exit(1)
	}
	sqsClient := sqs.NewFromConfig(cfg)
	queueURL := os.Getenv("SQS_QUEUE_URL")
	if queueURL == "" {
		logger.Error("SQS_QUEUE_URL is not set")
		os.Exit(1)
	}

	// 2. Init S3 (Reusing our shared storage logic)
	s3Client, err := storage.NewS3Client()
	if err != nil {
		logger.Error("Failed to init S3", "error", err)
		os.Exit(1)
	}

	// 3. Metrics Server
	go func() {
        http.Handle("/metrics", promhttp.Handler())
        http.ListenAndServe(":2112", nil) 
  }()

	// 4. Concurrency Limiter
	numCores := runtime.NumCPU()
	maxConcurrency := numCores - 1
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	
	logger.Info("Worker started", "cores", numCores, "max_jobs", maxConcurrency, "mode", "ABR_SQS")

	sem := make(chan token, maxConcurrency)

	for {
		// 5. Long Polling SQS
		msgOutput, err := sqsClient.ReceiveMessage(context.TODO(), &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 1,
			WaitTimeSeconds:     20,
			VisibilityTimeout:   600,
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

		// 6. Acquire Semaphore
		sem <- token{}
		activeJobs.Inc()

		go func(m types.Message) {
			defer func() { 
				<-sem 
				activeJobs.Dec()
			}()

			// Parse Job
			var job Job
			if err := json.Unmarshal([]byte(*m.Body), &job); err != nil {
				logger.Error("Invalid job format", "body", *m.Body)
				deleteMessage(sqsClient, queueURL, m.ReceiptHandle) 
				return
			}

			logger.Info("Processing job", "job_id", job.FileID)

			// Transcode
			if err := processVideoABR(ctxWithTimeout(), s3Client, job, logger); err != nil {
				logger.Error("Job failed", "job_id", job.FileID, "error", err)
			} else {
				logger.Info("Job complete", "job_id", job.FileID)
				deleteMessage(sqsClient, queueURL, m.ReceiptHandle)
			}
		}(msg)
	}
}

func deleteMessage(client *sqs.Client, queueURL string, receiptHandle *string) {
	_, err := client.DeleteMessage(context.TODO(), &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueURL),
		ReceiptHandle: receiptHandle,
	})
	if err != nil {
		fmt.Printf("Failed to delete SQS message: %v\n", err)
	}
}

func ctxWithTimeout() context.Context {
    return context.Background()
}

func processVideoABR(ctx context.Context, s3Client *s3.Client, job Job, logger *slog.Logger) error {
	start := time.Now()
	// Increase timeout for ABR transcoding (heavy CPU usage)
	procCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	// 1. Prepare Paths
	tempInput := filepath.Join("/tmp/uploads", job.FileID)
	// Base output directory. FFmpeg will create subdirs inside here.
	outputDir := filepath.Join("/tmp/hls", job.FileID)
	
	os.MkdirAll("/tmp/uploads", 0755)
	os.MkdirAll(outputDir, 0755)
	
	defer os.Remove(tempInput)
	defer os.RemoveAll(outputDir) // Clean up HLS files after upload

	// 2. Download from S3
	bucket := os.Getenv("S3_BUCKET") // "raw-videos"
	if bucket == "" {
		return fmt.Errorf("S3_BUCKET env var not set")
	}

	logger.Info("Downloading raw video...", "key", "uploads/"+job.FileID)
	destFile, err := os.Create(tempInput)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	// We close explicitly later to be safe, but defer ensures cleanup
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

	// 3. ABR Transcode (The "Complex" part)
	// We generate 3 variants: 1080p, 720p, 480p
	// Note: %v in segment filename matches the variant stream index (0, 1, 2)
	
	// Complex Filter: Splits input into 3 streams, scales them
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
		"-map", "a:0", "-c:a", "aac", "-b:a", "128k", "-ac", "2",
		
		// HLS Settings
		"-f", "hls",
		"-var_stream_map", "v:0,a:0 v:1,a:0 v:2,a:0",
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

	// 4. Upload Recursive Directory
	// The outputDir now contains:
	// - master.m3u8
	// - 0/ (1080p segments + playlist)
	// - 1/ (720p segments + playlist)
	// - 2/ (480p segments + playlist)
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

		// 1. Get relative path (e.g., "0/segment_001.ts")
		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}

		// 2. Create S3 Key (e.g., "uuid-123/0/segment_001.ts")
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
