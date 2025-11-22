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
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
    
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
        Buckets: prometheus.LinearBuckets(5, 5, 10), // 5s, 10s, 15s...
    })
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 1. Init Redis
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})

	// 2. Init S3 (Reusing our shared storage logic)
	s3Client, err := storage.NewS3Client()
	if err != nil {
		logger.Error("Failed to init S3", "error", err)
		os.Exit(1)
	}

	go func() {
        http.Handle("/metrics", promhttp.Handler())
        // We use port 2112, a common convention for auxiliary metrics
        http.ListenAndServe(":2112", nil) 
    }()

	logger.Info("Worker started. Metrics on :2112")

	for {
		result, err := rdb.BLPop(context.Background(), 0, "video_queue").Result()
		if err != nil {
			logger.Error("Redis connection failed", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		var job Job
		if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
			logger.Error("Invalid job format", "payload", result[1])
			continue
		}

		logger.Info("Processing job", "job", job)
		
		// Pass S3 client to process function
		if err := processVideo(ctxWithTimeout(), s3Client, job, logger); err != nil {
			logger.Error("Job failed", "job_id", job.FileID, "error", err)
		} else {
			logger.Info("Job complete", "job_id", job.FileID)
		}
	}
}

func ctxWithTimeout() context.Context {
    // Just a helper to get a context, we cancel it inside processVideo usually,
    // but for simplicity we pass a background here and handle timeout in processVideo
    return context.Background()
}

func processVideo(ctx context.Context, s3Client *s3.Client, job Job, logger *slog.Logger) error {
	start := time.Now()
	procCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

    // 1. Prepare Paths
	tempInput := filepath.Join("/tmp/uploads", job.FileID)
	outputDir := filepath.Join("/tmp/hls", job.FileID)
	os.MkdirAll("/tmp/uploads", 0755)
	os.MkdirAll(outputDir, 0755)

	// 2. DOWNLOAD FROM S3 (The Missing Link)
	logger.Info("Downloading from S3...", "key", "uploads/"+job.FileID)
	
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
			return fmt.Errorf("S3_BUCKET env var not set")
	}

	// Create a local file to write to
	destFile, err := os.Create(tempInput)
	if err != nil {
			return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer destFile.Close()
	defer os.Remove(tempInput) // Clean up input file after transcoding

	// Stream S3 object to file
	resp, err := s3Client.GetObject(procCtx, &s3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String("uploads/" + job.FileID),
	})
	if err != nil {
			return fmt.Errorf("failed to download from S3: %w", err)
	}
	defer resp.Body.Close()

	if _, err := io.Copy(destFile, resp.Body); err != nil {
			return fmt.Errorf("failed to write to temp file: %w", err)
	}

    // 3. Transcode (Same as before)
	cmd := exec.CommandContext(procCtx, "ffmpeg",
		"-i", tempInput,
		"-c:v", "libx264", "-preset", "veryfast", "-g", "120", "-sc_threshold", "0",
		"-c:a", "aac", "-b:a", "128k",
		"-f", "hls",
		"-hls_time", "4",
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(outputDir, "segment_%03d.ts"),
		filepath.Join(outputDir, "playlist.m3u8"),
	)

	cmd.Stderr = os.Stderr 
	
	logger.Info("Starting transcoding...")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	logger.Info("Transcoding complete. Uploading to S3...")
    
	// Upload the HLS folder
	err = uploadDirectoryToS3(procCtx, s3Client, outputDir, "processed-videos", job.FileID)
	if err != nil {
			return fmt.Errorf("failed to upload HLS to S3: %w", err)
	}

	// Cleanup local files to save space
	os.RemoveAll(outputDir)

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

		// Create relative path for S3 Key
		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}
		key := filepath.Join(s3Prefix, relPath)

		// Open file
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		// Set Content-Type based on extension
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
