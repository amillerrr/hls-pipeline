package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	"sync/atomic"
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
	"github.com/amillerrr/hls-pipeline/internal/storage"
)

// Configuration constants
const (
	// SQS settings
	SQSMaxMessages       = 1
	SQSWaitTimeSeconds   = 20
	SQSVisibilityTimeout = 900 // 15 minutes

	// Worker settings
	DefaultMaxConcurrent = 1
	MetricsPort          = 2112

	// Timeouts
	AWSConfigTimeout   = 10 * time.Second
	ShutdownTimeout    = 5 * time.Second
	RetryBackoffPeriod = 5 * time.Second

	// HLS settings
	HLSSegmentDuration = 6

	// File paths
	TempUploadDir = "/tmp/uploads"
	TempHLSDir    = "/tmp/hls"

	// Upload settings
	MaxConcurrentUploads = 20
)

// Video encoding parameters
type QualityPreset struct {
	Name      string
	Width     int
	Height    int
	Bitrate   string
	MaxRate   string
	BufSize   string
	AudioBPS  string
	Bandwidth int
}

// Video quality presets for FFmpeg
var qualityPresets = []QualityPreset{
	{"1080p", 1920, 1080, "5M", "5.5M", "7.5M", "192k", 5500000},
	{"720p", 1280, 720, "2.5M", "2.75M", "5M", "128k", 2750000},
	{"480p", 854, 480, "1M", "1.1M", "2M", "96k", 1100000},
}

var tracer = otel.Tracer("hls-worker")

// Custom errors
var (
	ErrJobParseFailed  = errors.New("failed to parse job")
	ErrDownloadFailed  = errors.New("failed to download video")
	ErrTranscodeFailed = errors.New("failed to transcode video")
	ErrUploadFailed    = errors.New("failed to upload HLS files")
	ErrFFmpegFailed    = errors.New("ffmpeg failed")
	ErrContextCanceled = errors.New("context canceled")
)

// Metrics
var (
	videosProcessed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hls_videos_processed_total",
			Help: "Total number of videos processed",
		},
		[]string{"status"},
	)
	processingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "hls_video_processing_duration_seconds",
			Help:    "Time taken to process videos",
			Buckets: []float64{10, 30, 60, 120, 300, 600},
		},
		[]string{"resolution"},
	)
	qualityScore = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "hls_video_quality_score",
			Help: "Quality score (SSIM) for processed videos",
		},
		[]string{"metric_type"},
	)
	activeJobs = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_active_jobs",
			Help: "Number of currently processing jobs",
		},
	)
	downloadDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "hls_video_download_duration_seconds",
			Help:    "Time taken to download videos from S3",
			Buckets: []float64{1, 5, 10, 30, 60, 120},
		},
	)
	uploadDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "hls_video_upload_duration_seconds",
			Help:    "Time taken to upload HLS files to S3",
			Buckets: []float64{1, 5, 10, 30, 60, 120},
		},
	)
)

func init() {
	prometheus.MustRegister(videosProcessed)
	prometheus.MustRegister(processingDuration)
	prometheus.MustRegister(qualityScore)
	prometheus.MustRegister(activeJobs)
	prometheus.MustRegister(downloadDuration)
	prometheus.MustRegister(uploadDuration)
}

type Worker struct {
	s3Client        *s3.Client
	sqsClient       *sqs.Client
	videoRepo       *storage.VideoRepository
	sqsQueueURL     string
	rawBucket       string
	processedBucket string
	cdnDomain       string
	log             *slog.Logger
	maxConcurrent   int
	metricsServer   *http.Server
}

type VideoJob struct {
	VideoID  string `json:"videoId"`
	S3Key    string `json:"s3Key"`
	Bucket   string `json:"bucket"`
	Filename string `json:"filename"`
}

// Validate the video job fields
func (j *VideoJob) Validate() error {
	if j.VideoID == "" {
		return errors.New("videoId is required")
	}
	if j.S3Key == "" {
		return errors.New("s3Key is required")
	}
	if j.Bucket == "" {
		return errors.New("bucket is required")
	}
	return nil
}

func main() {
	log := logger.New()
	slog.SetDefault(log)

	if err := godotenv.Load(); err != nil {
		logger.Info(context.Background(), log, "No .env file found, relying on system ENV variables")
	}

	// Initialize tracing
	shutdownTracer, err := observability.InitTracer(context.Background(), "hls-worker")
	if err != nil {
		logger.Error(context.Background(), log, "Failed to initialize tracer", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer cancel()
		if err := shutdownTracer(shutdownCtx); err != nil {
			logger.Error(context.Background(), log, "Failed to shutdown tracer", "error", err)
		}
	}()

	// AWS Config and SQS Initialization
	ctx, cancel := context.WithTimeout(context.Background(), AWSConfigTimeout)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		logger.Error(context.Background(), log, "Failed to load AWS config", "error", err)
		os.Exit(1)
	}
	otelaws.AppendMiddlewares(&cfg.APIOptions)

	maxConcurrent := DefaultMaxConcurrent
	if mc := os.Getenv("MAX_CONCURRENT_JOBS"); mc != "" {
		if parsed, err := strconv.Atoi(mc); err == nil && parsed > 0 {
			maxConcurrent = parsed
		}
	}

	videoRepo, err := storage.NewVideoRepository(context.Background())
	if err != nil {
		logger.Error(context.Background(), log, "Failed to initialize video repository", "error", err)
		os.Exit(1)
	}

	worker := &Worker{
		s3Client:        s3.NewFromConfig(cfg),
		sqsClient:       sqs.NewFromConfig(cfg),
		videoRepo:       videoRepo,
		sqsQueueURL:     os.Getenv("SQS_QUEUE_URL"),
		rawBucket:       os.Getenv("S3_BUCKET"),
		processedBucket: os.Getenv("PROCESSED_BUCKET"),
		cdnDomain:       os.Getenv("CDN_DOMAIN"),
		log:             log,
		maxConcurrent:   maxConcurrent,
	}

	// Validate required configuration
	if err := worker.validateConfig(); err != nil {
		logger.Error(context.Background(), log, "Invalid configuration", "error", err)
		os.Exit(1)
	}

	// Start metrics server
	go worker.startMetricsServer()

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

	// Shutdown metrics server gracefully
	if worker.metricsServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), ShutdownTimeout)
		defer shutdownCancel()
		if err := worker.metricsServer.Shutdown(shutdownCtx); err != nil {
			logger.Error(context.Background(), log, "Failed to shutdown metrics server", "error", err)
		}
	}
}

func (w *Worker) validateConfig() error {
	if w.sqsQueueURL == "" {
		return errors.New("SQS_QUEUE_URL is required")
	}
	if w.rawBucket == "" {
		return errors.New("S3_BUCKET is required")
	}
	if w.processedBucket == "" {
		return errors.New("PROCESSED_BUCKET is required")
	}
	if w.cdnDomain == "" {
		return errors.New("CDN_DOMAIN is required")
	}
	return nil
}

func (w *Worker) startMetricsServer() {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
		if _, err := rw.Write([]byte(`{"status":"healthy"}`)); err != nil {
			logger.Error(r.Context(), w.log, "Failed to write health response", "error", err)
		}
	})

	w.metricsServer = &http.Server{
		Addr:              fmt.Sprintf(":%d", MetricsPort),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info(context.Background(), w.log, "Starting metrics server", "port", MetricsPort)
	if err := w.metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error(context.Background(), w.log, "Metrics server error", "error", err)
	}
}

func (w *Worker) pollQueue(ctx context.Context) {
	logger.Info(ctx, w.log, "Starting queue polling", "queueURL", w.sqsQueueURL, "maxConcurrent", w.maxConcurrent)

	sem := make(chan struct{}, w.maxConcurrent)
	var wg sync.WaitGroup

messageLoop:
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
			MaxNumberOfMessages: SQSMaxMessages,
			WaitTimeSeconds:     SQSWaitTimeSeconds,
			VisibilityTimeout:   SQSVisibilityTimeout,
		})
		if err != nil {
			if ctx.Err() != nil {
				continue // Shutting down
			}
			logger.Error(ctx, w.log, "Failed to receive messages", "error", err)
			time.Sleep(RetryBackoffPeriod)
			continue
		}

		for _, msg := range result.Messages {
			select {
			case sem <- struct{}{}:
				wg.Add(1)
				go func(msg types.Message) {
					defer wg.Done()
					defer func() { <-sem }() // Release semaphore

					activeJobs.Inc()
					defer activeJobs.Dec()

					if err := w.processMessage(ctx, msg); err != nil {
						logger.Error(ctx, w.log, "Failed to process message", "error", err, "messageId", safeStringDeref(msg.MessageId))
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
			case <-ctx.Done():
				logger.Info(ctx, w.log, "Context cancelled, stopping message processing")
				break messageLoop
			}
		}
	}
}

func safeStringDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (w *Worker) processMessage(ctx context.Context, msg types.Message) error {
	ctx, span := tracer.Start(ctx, "process-message")
	defer span.End()

	if msg.Body == nil {
		return fmt.Errorf("%w: empty message body", ErrJobParseFailed)
	}

	var job VideoJob
	if err := json.Unmarshal([]byte(*msg.Body), &job); err != nil {
		return fmt.Errorf("%w: %v", ErrJobParseFailed, err)
	}

	if err := job.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrJobParseFailed, err)
	}

	span.SetAttributes(
		attribute.String("video.id", job.VideoID),
		attribute.String("video.s3_key", job.S3Key),
		attribute.String("video.filename", job.Filename),
	)

	logger.Info(ctx, w.log, "Processing video",
		"videoId", job.VideoID,
		"s3Key", job.S3Key,
		"filename", job.Filename,
	)

	if err := w.videoRepo.UpdateVideoProcessing(ctx, job.VideoID); err != nil {
		logger.Warn(ctx, w.log, "Failed to update video status to processing", "videoId", job.VideoID, "error", err)
	}

	var processingErr error
	defer func() {
		if processingErr != nil {
			if failErr := w.videoRepo.FailVideoProcessing(ctx, job.VideoID, processingErr.Error()); failErr != nil {
				logger.Error(ctx, w.log, "Failed to mark video as failed", "videoId", job.VideoID, "error", failErr)
			}
		}
	}()

	start := time.Now()

	// Download video from S3
	downloadStart := time.Now()
	localPath, err := w.downloadVideo(ctx, job)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}
	downloadDuration.Observe(time.Since(downloadStart).Seconds())
	defer func() {
		if removeErr := os.Remove(localPath); removeErr != nil && !os.IsNotExist(removeErr) {
			logger.Warn(ctx, w.log, "Failed to remove temp file", "path", localPath, "error", removeErr)
		}
	}()

	// Check for context cancellation before transcoding
	if ctx.Err() != nil {
		return fmt.Errorf("%w: before transcoding", ErrContextCanceled)
	}

	// Transcode to HLS
	hlsDir, err := w.transcodeToHLS(ctx, job.VideoID, localPath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTranscodeFailed, err)
	}
	defer func() {
		if removeErr := os.RemoveAll(hlsDir); removeErr != nil {
			logger.Warn(ctx, w.log, "Failed to remove HLS dir", "path", hlsDir, "error", removeErr)
		}
	}()

	// Check for context cancellation before uploading
	if ctx.Err() != nil {
		return fmt.Errorf("%w: before upload", ErrContextCanceled)
	}

	// Upload HLS files to S3
	uploadStart := time.Now()
	if err := w.uploadHLSFiles(ctx, job.VideoID, hlsDir); err != nil {
		return fmt.Errorf("%w: %v", ErrUploadFailed, err)
	}
	uploadDuration.Observe(time.Since(uploadStart).Seconds())

	duration := time.Since(start).Seconds()
	processingDuration.WithLabelValues("all").Observe(duration)

	hlsPrefix := fmt.Sprintf("hls/%s/", job.VideoID)
	playbackURL := fmt.Sprintf("https://%s/hls/%s/master.m3u8", w.cdnDomain, job.VideoID)

	// Convert quality presets to storage format
	dbPresets := make([]storage.QualityPreset, len(qualityPresets))
	for i, p := range qualityPresets {
		dbPresets[i] = storage.QualityPreset{
			Name:    p.Name,
			Width:   p.Width,
			Height:  p.Height,
			Bitrate: p.Bandwidth,
		}
	}

	if err := w.videoRepo.CompleteVideoProcessing(ctx, job.VideoID, playbackURL, hlsPrefix, dbPresets); err != nil {
		logger.Error(ctx, w.log, "Failed to mark video as completed in DynamoDB", "videoId", job.VideoID, "error", err)
	}

	logger.Info(ctx, w.log, "Video processed successfully",
		"videoId", job.VideoID,
		"filename", job.Filename,
		"durationSeconds", duration,
		"playbackURL", playbackURL,
	)

	return nil
}

func (w *Worker) downloadVideo(ctx context.Context, job VideoJob) (string, error) {
	ctx, span := tracer.Start(ctx, "download-video")
	defer span.End()

	// Ensure temp directory exists
	if err := os.MkdirAll(TempUploadDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Create temp file
	ext := filepath.Ext(job.S3Key)
	tmpFile, err := os.CreateTemp(TempUploadDir, fmt.Sprintf("video-*%s", ext))
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Download from S3
	result, err := w.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(job.Bucket),
		Key:    aws.String(job.S3Key),
	})
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to get object from S3: %w", err)
	}
	defer result.Body.Close()

	// Copy to file
	written, err := io.Copy(tmpFile, result.Body)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("failed to close temp file: %w", err)
	}

	span.SetAttributes(attribute.Int64("video.size_bytes", written))
	logger.Info(ctx, w.log, "Downloaded video",
		"videoId", job.VideoID,
		"sizeBytes", written,
	)

	return tmpPath, nil
}

func (w *Worker) transcodeToHLS(ctx context.Context, videoID string, inputPath string) (string, error) {
	ctx, span := tracer.Start(ctx, "transcode-hls")
	defer span.End()

	// Create output directory
	hlsDir := filepath.Join(TempHLSDir, videoID)

	// Create subdirectories for each quality level
	for _, preset := range qualityPresets {
		dirPath := filepath.Join(hlsDir, preset.Name)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			os.RemoveAll(hlsDir)
			return "", fmt.Errorf("failed to create HLS subdir %s: %w", preset.Name, err)
		}
	}

	// Run FFmpeg transcoding
	if err := w.runFFmpeg(ctx, inputPath, hlsDir); err != nil {
		os.RemoveAll(hlsDir)
		return "", err
	}

	// Generate master playlist
	if err := w.generateMasterPlaylist(hlsDir); err != nil {
		os.RemoveAll(hlsDir)
		return "", fmt.Errorf("failed to generate master playlist: %w", err)
	}

	// Calculate quality metrics
	w.calculateQualityMetrics(ctx, inputPath, hlsDir)

	return hlsDir, nil
}

// Generate the FFmpeg filter_complex string
func buildFilterComplex(presets []QualityPreset) string {
	n := len(presets)
	if n == 0 {
		return ""
	}

	// Build split outputs: [v1][v2][v3]...
	var splitOutputs strings.Builder
	for i := 1; i <= n; i++ {
		splitOutputs.WriteString(fmt.Sprintf("[v%d]", i))
	}

	// Build the complete filter complex
	var filter strings.Builder
	filter.WriteString(fmt.Sprintf("[0:v]split=%d%s;", n, splitOutputs.String()))

	// Build scale filters for each preset
	for i, preset := range presets {
		filter.WriteString(fmt.Sprintf("[v%d]scale=%d:%d[v%dout]",
			i+1, preset.Width, preset.Height, i+1))
		if i < n-1 {
			filter.WriteString(";")
		}
	}

	return filter.String()
}

func (w *Worker) runFFmpeg(ctx context.Context, inputPath, hlsDir string) error {
	ctx, span := tracer.Start(ctx, "ffmpeg-transcode")
	defer span.End()

	// Build FFmpeg args using quality presets
	args := []string{
		"-i", inputPath,
		"-preset", "veryfast",
		"-c:v", "libx264",
		"-profile:v", "main",
		"-level", "4.1",
		"-g", "100",
		"-keyint_min", "100",
		"-sc_threshold", "0",
		"-flags", "+cgop",
		"-filter_complex", buildFilterComplex(qualityPresets),
	}

	// Add output streams for each quality preset
	for i, preset := range qualityPresets {
		streamArgs := []string{
			"-map", fmt.Sprintf("[v%dout]", i+1), "-map", "0:a?",
			fmt.Sprintf("-c:v:%d", i), "libx264",
			fmt.Sprintf("-b:v:%d", i), preset.Bitrate,
			fmt.Sprintf("-maxrate:v:%d", i), preset.MaxRate,
			fmt.Sprintf("-bufsize:v:%d", i), preset.BufSize,
			fmt.Sprintf("-c:a:%d", i), "aac",
			fmt.Sprintf("-b:a:%d", i), preset.AudioBPS,
			"-hls_time", fmt.Sprintf("%d", HLSSegmentDuration),
			"-hls_list_size", "0",
			"-hls_segment_filename", filepath.Join(hlsDir, preset.Name, "seg_%03d.ts"),
			filepath.Join(hlsDir, preset.Name, "playlist.m3u8"),
		}
		args = append(args, streamArgs...)
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
		if _, copyErr := io.Copy(io.Discard, stdoutPipe); copyErr != nil {
			logger.Warn(ctx, w.log, "Failed to drain stdout", "error", copyErr)
		}
	}()

	// Wait for command to complete
	cmdErr := cmd.Wait()
	wg.Wait()

	if cmdErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: context canceled", ErrFFmpegFailed)
		}
		return fmt.Errorf("%w: %v", ErrFFmpegFailed, cmdErr)
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
			if strings.Contains(line, "frame=") || strings.Contains(line, "time=") {
				logger.Debug(ctx, w.log, "FFmpeg progress", "output", line)
			} else if strings.Contains(line, "error") || strings.Contains(line, "Error") {
				logger.Warn(ctx, w.log, "FFmpeg warning", "output", line)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		logger.Warn(ctx, w.log, "FFmpeg output scanner error", "error", err)
	}
}

func (w *Worker) generateMasterPlaylist(hlsDir string) error {
	var builder strings.Builder
	builder.WriteString("#EXTM3U\n")
	builder.WriteString("#EXT-X-VERSION:3\n")

	for _, preset := range qualityPresets {
		builder.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n",
			preset.Bandwidth, preset.Width, preset.Height))
		builder.WriteString(fmt.Sprintf("%s/playlist.m3u8\n", preset.Name))
	}
	return os.WriteFile(filepath.Join(hlsDir, "master.m3u8"), []byte(builder.String()), 0644)
}

func (w *Worker) calculateQualityMetrics(ctx context.Context, inputPath, hlsDir string) {
	ctx, span := tracer.Start(ctx, "calculate-quality")
	defer span.End()

	// Extract a frame from 720p output and compare to source
	refFrame := filepath.Join(hlsDir, "ref_frame.png")
	distFrame := filepath.Join(hlsDir, "dist_frame.png")

	defer func() {
		os.Remove(refFrame)
		os.Remove(distFrame)
	}()

	// Extract frame from source
	err := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-ss", "00:00:01", "-i", inputPath,
		"-vf", "scale=1280:720", "-vframes", "1", refFrame,
	).Run()
	if err != nil {
		logger.Warn(ctx, w.log, "Failed to extract reference frame (video too short?)", "error", err)
		return
	}

	// Extract frame from 720p output at 1s
	playlist720 := filepath.Join(hlsDir, "720p", "playlist.m3u8")
	err = exec.CommandContext(ctx, "ffmpeg",
		"-y", "-ss", "00:00:01", "-i", playlist720,
		"-vframes", "1", distFrame,
	).Run()
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
		ssimStr := strings.TrimSpace(outputStr[idx+4 : min(idx+10, len(outputStr))])
		if ssim, parseErr := strconv.ParseFloat(ssimStr, 64); parseErr == nil {
			qualityScore.WithLabelValues("720p_vs_source").Set(ssim)
			logger.Info(ctx, w.log, "SSIM score", "value", ssim)
		}
	}
}

func (w *Worker) uploadHLSFiles(ctx context.Context, videoID, hlsDir string) error {
	ctx, span := tracer.Start(ctx, "upload-hls")
	defer span.End()

	// Atomic counters for thread safety
	var filesUploaded atomic.Int64
	var totalBytes atomic.Int64
	var firstErr atomic.Pointer[error]

	// Concurrency control
	sem := make(chan struct{}, MaxConcurrentUploads)
	var wg sync.WaitGroup

	walkErr := filepath.Walk(hlsDir, func(path string, info os.FileInfo, err error) error {
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

		if firstErr.Load() != nil {
			return nil
		}

		// Acquire semaphore (blocks if limit reached)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return fmt.Errorf("%w: during upload walk", ErrContextCanceled)
		}

		wg.Add(1)

		go func(filePath string, fileInfo os.FileInfo) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			// Check if previous error occurred
			if firstErr.Load() != nil {
				return
			}

			// Calculate Key
			relPath, err := filepath.Rel(hlsDir, filePath)
			if err != nil {
				wrappedErr := fmt.Errorf("failed to get relative path: %w", err)
				firstErr.CompareAndSwap(nil, &wrappedErr)
				return
			}
			s3Key := fmt.Sprintf("hls/%s/%s", videoID, relPath)

			// Open File
			file, err := os.Open(filePath)
			if err != nil {
				wrappedErr := fmt.Errorf("failed to open file %s: %w", filePath, err)
				firstErr.CompareAndSwap(nil, &wrappedErr)
				return
			}
			defer file.Close()

			// Determine Content Type
			contentType := "application/octet-stream"
			switch {
			case strings.HasSuffix(filePath, ".m3u8"):
				contentType = "application/vnd.apple.mpegurl"
			case strings.HasSuffix(filePath, ".ts"):
				contentType = "video/MP2T"
			}

			// Upload
			_, err = w.s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      aws.String(w.processedBucket),
				Key:         aws.String(s3Key),
				Body:        file,
				ContentType: aws.String(contentType),
			})
			if err != nil {
				wrappedErr := fmt.Errorf("failed to upload %s: %w", s3Key, err)
				firstErr.CompareAndSwap(nil, &wrappedErr)
				return
			}

			// Update Metrics Atomically
			filesUploaded.Add(1)
			totalBytes.Add(fileInfo.Size())

			// Use Debug level to reduce log noise
			logger.Debug(ctx, w.log, "Uploaded file", "key", s3Key)

		}(path, info)

		return nil
	})

	// Wait for all uploads to complete
	wg.Wait()

	// Check for walk errors
	if walkErr != nil {
		return walkErr
	}

	// Check for async upload errors
	if errPtr := firstErr.Load(); errPtr != nil {
		return *errPtr
	}

	uploaded := filesUploaded.Load()
	bytes := totalBytes.Load()

	span.SetAttributes(
		attribute.Int64("files.uploaded", uploaded),
		attribute.Int64("bytes.total", bytes),
	)

	logger.Info(ctx, w.log, "HLS upload complete",
		"videoId", videoID,
		"filesUploaded", uploaded,
		"totalBytes", bytes,
	)
	return nil
}
