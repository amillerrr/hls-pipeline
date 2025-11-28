package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/amillerrr/hls-pipeline/internal/logger"
	"github.com/amillerrr/hls-pipeline/internal/observability"
)

var tracer = otel.Tracer("eye-worker")

// Metrics
var (
	videosProcessed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "eye_videos_processed_total",
			Help: "Total number of videos processed",
		},
		[]string{"status"},
	)
	processingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "eye_video_processing_duration_seconds",
			Help:    "Time taken to process videos",
			Buckets: []float64{10, 30, 60, 120, 300, 600},
		},
		[]string{"resolution"},
	)
	qualityScore = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "eye_video_quality_score",
			Help: "Quality score (SSIM) for processed videos",
		},
		[]string{"metric_type"},
	)
)

func init() {
	prometheus.MustRegister(videosProcessed)
	prometheus.MustRegister(processingDuration)
	prometheus.MustRegister(qualityScore)
}

type Worker struct {
	s3Client        *s3.Client
	sqsClient       *sqs.Client
	sqsQueueURL     string
	rawBucket       string
	processedBucket string
	log             *slog.Logger
	maxConcurrent   int
}

type VideoJob struct {
	VideoID  string `json:"videoId"`
	S3Key    string `json:"s3Key"`
	Bucket   string `json:"bucket"`
	Filename string `json:"filename"`
}

func main() {
	log := logger.New()
	slog.SetDefault(log)

	if err := godotenv.Load(); err != nil {
		logger.Info(context.Background(), log, "No .env file found, relying on system ENV variables")
	}

	// Initialize tracing
	shutdownTracer := observability.InitTracer(context.Background(), "eye-worker")
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracer(shutdownCtx); err != nil {
			logger.Error(context.Background(), log, "Failed to shutdown tracer", "error", err)
		}
	}()

	// AWS Config and SQS Initialization
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		logger.Error(context.Background(), log, "Failed to load AWS config", "error", err)
		os.Exit(1)
	}
	otelaws.AppendMiddlewares(&cfg.APIOptions)

	maxConcurrent := 1
	if mc := os.Getenv("MAX_CONCURRENT_JOBS"); mc != "" {
		if parsed, err := strconv.Atoi(mc); err == nil && parsed > 0 {
			maxConcurrent = parsed
		}
	}

	worker := &Worker{
		s3Client:        s3.NewFromConfig(cfg),
		sqsClient:       sqs.NewFromConfig(cfg),
		sqsQueueURL:     os.Getenv("SQS_QUEUE_URL"),
		rawBucket:       os.Getenv("S3_BUCKET"),
		processedBucket: os.Getenv("PROCESSED_BUCKET"),
		log:             log,
		maxConcurrent:   maxConcurrent,
	}

	// Start metrics server
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("ok")); err != nil {
				logger.Error(r.Context(), log, "Failed to write health response", "error", err)
			}
		})
		logger.Info(context.Background(), log, "Starting metrics server", "port", "2112")
		if err := http.ListenAndServe(":2112", nil); err != nil {
			logger.Error(context.Background(), log, "Metrics server error", "error", err)
		}
	}()

	// Graceful shutdown
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-quit
		logger.Info(context.Background(), log, "Shutting down worker...")
		cancel()
	}()

	// Start polling
	worker.pollQueue(ctx)
}

func (w *Worker) pollQueue(ctx context.Context) {
	logger.Info(ctx, w.log, "Starting queue polling", "queueURL", w.sqsQueueURL)

	sem := make(chan struct{}, w.maxConcurrent)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			logger.Info(ctx, w.log, "Waiting for in-progress jobs to complete...")
			wg.Wait()
			logger.Info(ctx, w.log, "All jobs completed, shutting down")
			return
		default:
		}

		// Receive messages
		result, err := w.sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(w.sqsQueueURL),
			MaxNumberOfMessages: 1,
			WaitTimeSeconds:     20,
			VisibilityTimeout:   900, // 15 minutes
		})
		if err != nil {
			if ctx.Err() != nil {
				continue // Shutting down
			}
			logger.Error(ctx, w.log, "Failed to receive messages", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, msg := range result.Messages {
			sem <- struct{}{} // Acquire semaphore
			wg.Add(1)

			go func(msg types.Message) {
				defer wg.Done()
				defer func() { <-sem }() // Release semaphore

				if err := w.processMessage(ctx, msg); err != nil {
					logger.Error(ctx, w.log, "Failed to process message", "error", err)
					videosProcessed.WithLabelValues("failed").Inc()
				} else {
					// Delete message on success
					_, delErr := w.sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
						QueueUrl:      aws.String(w.sqsQueueURL),
						ReceiptHandle: msg.ReceiptHandle,
					})
					if delErr != nil {
						logger.Error(ctx, w.log, "Failed to delete message", "error", delErr)
					}
					videosProcessed.WithLabelValues("success").Inc()
				}
			}(msg)
		}
	}
}

func (w *Worker) processMessage(ctx context.Context, msg types.Message) error {
	ctx, span := tracer.Start(ctx, "process-message")
	defer span.End()

	var job VideoJob
	if err := json.Unmarshal([]byte(*msg.Body), &job); err != nil {
		return fmt.Errorf("failed to parse job: %w", err)
	}

	span.SetAttributes(
		attribute.String("video.id", job.VideoID),
		attribute.String("video.s3_key", job.S3Key),
	)

	logger.Info(ctx, w.log, "Processing video", "videoId", job.VideoID, "s3Key", job.S3Key)

	start := time.Now()

	// Download video from S3
	localPath, err := w.downloadVideo(ctx, job)
	if err != nil {
		return fmt.Errorf("failed to download video: %w", err)
	}
	defer os.Remove(localPath)

	// Transcode to HLS
	hlsDir, err := w.transcodeToHLS(ctx, job.VideoID, localPath)
	if err != nil {
		return fmt.Errorf("failed to transcode: %w", err)
	}
	defer os.RemoveAll(hlsDir)

	// Upload HLS files to S3
	if err := w.uploadHLSFiles(ctx, job.VideoID, hlsDir); err != nil {
		return fmt.Errorf("failed to upload HLS: %w", err)
	}

	duration := time.Since(start).Seconds()
	processingDuration.WithLabelValues("all").Observe(duration)

	logger.Info(ctx, w.log, "Video processed successfully",
		"videoId", job.VideoID,
		"durationSeconds", duration,
	)

	return nil
}

func (w *Worker) downloadVideo(ctx context.Context, job VideoJob) (string, error) {
	ctx, span := tracer.Start(ctx, "download-video")
	defer span.End()

	// Create temp file
	ext := filepath.Ext(job.S3Key)
	tmpFile, err := os.CreateTemp("/tmp/uploads", fmt.Sprintf("video-*%s", ext))
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tmpFile.Close()

	// Download from S3
	result, err := w.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(job.Bucket),
		Key:    aws.String(job.S3Key),
	})
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to get object: %w", err)
	}
	defer result.Body.Close()

	if _, err := io.Copy(tmpFile, result.Body); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return tmpFile.Name(), nil
}

func (w *Worker) transcodeToHLS(ctx context.Context, videoID string, inputPath string) (string, error) {
	ctx, span := tracer.Start(ctx, "transcode-hls")
	defer span.End()

	// Create output directory
	hlsDir := filepath.Join("/tmp/hls", videoID)
	if err := os.MkdirAll(hlsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create HLS dir: %w", err)
	}

	// Run FFmpeg transcoding
	if err := w.runFFmpeg(ctx, inputPath, hlsDir); err != nil {
		os.RemoveAll(hlsDir)
		return "", err
	}

	// Generate master playlist
	if err := w.generateMasterPlaylist(hlsDir); err != nil {
		os.RemoveAll(hlsDir)
		return "", err
	}

	// Calculate quality metrics
	w.calculateQualityMetrics(ctx, inputPath, hlsDir)

	return hlsDir, nil
}

func (w *Worker) runFFmpeg(ctx context.Context, inputPath, hlsDir string) error {
	ctx, span := tracer.Start(ctx, "ffmpeg-transcode")
	defer span.End()

	// Multi-bitrate HLS encoding
	args := []string{
		"-i", inputPath,
		"-filter_complex",
		"[0:v]split=3[v1][v2][v3];" +
			"[v1]scale=1920:1080[v1out];" +
			"[v2]scale=1280:720[v2out];" +
			"[v3]scale=854:480[v3out]",
		// 1080p
		"-map", "[v1out]", "-map", "0:a?",
		"-c:v:0", "libx264", "-b:v:0", "5M", "-maxrate:v:0", "5.5M", "-bufsize:v:0", "10M",
		"-c:a:0", "aac", "-b:a:0", "192k",
		"-hls_time", "6", "-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(hlsDir, "1080p", "seg_%03d.ts"),
		filepath.Join(hlsDir, "1080p", "playlist.m3u8"),
		// 720p
		"-map", "[v2out]", "-map", "0:a?",
		"-c:v:1", "libx264", "-b:v:1", "2.5M", "-maxrate:v:1", "2.75M", "-bufsize:v:1", "5M",
		"-c:a:1", "aac", "-b:a:1", "128k",
		"-hls_time", "6", "-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(hlsDir, "720p", "seg_%03d.ts"),
		filepath.Join(hlsDir, "720p", "playlist.m3u8"),
		// 480p
		"-map", "[v3out]", "-map", "0:a?",
		"-c:v:2", "libx264", "-b:v:2", "1M", "-maxrate:v:2", "1.1M", "-bufsize:v:2", "2M",
		"-c:a:2", "aac", "-b:a:2", "96k",
		"-hls_time", "6", "-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(hlsDir, "480p", "seg_%03d.ts"),
		filepath.Join(hlsDir, "480p", "playlist.m3u8"),
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Monitor stderr
	go func() {
		defer wg.Done()
		w.monitorFFmpegOutput(ctx, stderrPipe)
	}()

	// Drain stdout
	go func() {
		defer wg.Done()
		if _, err := io.Copy(io.Discard, stdoutPipe); err != nil {
			logger.Warn(ctx, w.log, "Failed to drain stdout", "error", err)
		}
	}()

	// Wait for command to complete
	cmdErr := cmd.Wait()
	wg.Wait()

	if cmdErr != nil {
		return fmt.Errorf("ffmpeg failed: %w", cmdErr)
	}

	return nil
}

func (w *Worker) monitorFFmpegOutput(ctx context.Context, r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			line := scanner.Text()
			// Log progress lines
			if strings.Contains(line, "frame=") || strings.Contains(line, "time=") {
				logger.Debug(ctx, w.log, "FFmpeg progress", "output", line)
			} else if strings.Contains(line, "error") || strings.Contains(line, "Error") {
				logger.Warn(ctx, w.log, "FFmpeg warning", "output", line)
			}
		}
	}
}

func (w *Worker) generateMasterPlaylist(hlsDir string) error {
	masterContent := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-STREAM-INF:BANDWIDTH=5500000,RESOLUTION=1920x1080
1080p/playlist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2750000,RESOLUTION=1280x720
720p/playlist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1100000,RESOLUTION=854x480
480p/playlist.m3u8
`
	return os.WriteFile(filepath.Join(hlsDir, "master.m3u8"), []byte(masterContent), 0644)
}

func (w *Worker) calculateQualityMetrics(ctx context.Context, inputPath, hlsDir string) {
	ctx, span := tracer.Start(ctx, "calculate-quality")
	defer span.End()

	// Extract a frame from 720p output and compare to source
	refFrame := filepath.Join(hlsDir, "ref_frame.png")
	distFrame := filepath.Join(hlsDir, "dist_frame.png")

	defer os.Remove(refFrame)
	defer os.Remove(distFrame)

	// Extract frame from source
	err := exec.CommandContext(ctx, "ffmpeg", "-y", "-ss", "00:00:01", "-i", inputPath,
		"-vf", "scale=1280:720", "-vframes", "1", refFrame).Run()
	if err != nil {
		logger.Warn(ctx, w.log, "Failed to extract reference frame (video too short?)", "error", err)
		return
	}

	// Extract frame from 720p output at 1s
	playlist720 := filepath.Join(hlsDir, "720p", "playlist.m3u8")
	err = exec.CommandContext(ctx, "ffmpeg", "-y", "-ss", "00:00:01", "-i", playlist720,
		"-vframes", "1", distFrame).Run()
	if err != nil {
		logger.Warn(ctx, w.log, "Failed to extract dist frame", "error", err)
		return
	}

	// Calculate SSIM
	ssimCmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", refFrame, "-i", distFrame,
		"-lavfi", "ssim", "-f", "null", "-")

	output, err := ssimCmd.CombinedOutput()
	if err != nil {
		logger.Warn(ctx, w.log, "Failed to calculate SSIM", "error", err)
		return
	}

	// Parse SSIM from output
	outputStr := string(output)
	if idx := strings.Index(outputStr, "All:"); idx != -1 {
		ssimStr := strings.TrimSpace(outputStr[idx+4 : idx+10])
		if ssim, err := strconv.ParseFloat(ssimStr, 64); err == nil {
			qualityScore.WithLabelValues("720p_vs_source").Set(ssim)
			logger.Info(ctx, w.log, "SSIM score", "value", ssim)
		}
	}
}

func (w *Worker) uploadHLSFiles(ctx context.Context, videoID, hlsDir string) error {
	ctx, span := tracer.Start(ctx, "upload-hls")
	defer span.End()

	return filepath.Walk(hlsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Skip temporary files
		if strings.HasSuffix(path, ".png") {
			return nil
		}

		relPath, _ := filepath.Rel(hlsDir, path)
		s3Key := fmt.Sprintf("hls/%s/%s", videoID, relPath)

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}
		defer file.Close()

		contentType := "application/octet-stream"
		if strings.HasSuffix(path, ".m3u8") {
			contentType = "application/vnd.apple.mpegurl"
		} else if strings.HasSuffix(path, ".ts") {
			contentType = "video/MP2T"
		}

		_, err = w.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(w.processedBucket),
			Key:         aws.String(s3Key),
			Body:        file,
			ContentType: aws.String(contentType),
		})
		if err != nil {
			return fmt.Errorf("failed to upload %s: %w", s3Key, err)
		}

		logger.Debug(ctx, w.log, "Uploaded file", "key", s3Key)
		return nil
	})
}
