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
	"regexp"
	"runtime"
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
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/amillerrr/hls-pipeline/internal/observability"
	"github.com/amillerrr/hls-pipeline/internal/storage"
	"github.com/amillerrr/hls-pipeline/internal/logger"
)

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
	videoQuality = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "transcode_quality_ssim",
		Help: "Structural Similarity Index",
	}, []string{"file_id", "resolution"})
	currentBitrate = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "transcode_bitrate_kbps",
		Help: "Current bitrate of the transcoding process",
	}, []string{"file_id"})
)

// Pre-compiled regex for performance 
var (
	reBitrate = regexp.MustCompile(`bitrate=\s*([\d\.]+)kbits/s`)
	reSSIM    = regexp.MustCompile(`Y:([\d\.]+)`)
)

type Job struct {
	FileID string `json:"file_id"`
}

// Semaphore token
type token struct{}

func main() {
	// Initialize Logger
	log := logger.New()
	slog.SetDefault(log)

	// Load .env file
	if err := godotenv.Load(); err != nil {
		logger.Info(context.Background(), log, "No .env file found, relying on system ENV variables")
	} 

	shutdownTracer := observability.InitTracer(context.Background(), "eye-worker")
	defer func() {
		if err := shutdownTracer(context.Background()); err != nil {
			logger.Error(context.Background(), log, "Failed to shutdown tracer", "error", err)
		}
	}()

	// AWS Config and SQS Initialization
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		logger.Error(context.Background(), log, "Failed to load AWS config", "error", err)
		os.Exit(1)
	}
	otelaws.AppendMiddlewares(&cfg.APIOptions)
	sqsClient := sqs.NewFromConfig(cfg)
	queueURL := os.Getenv("SQS_QUEUE_URL")
	if queueURL == "" {
		logger.Error(context.Background(), log, "SQS_QUEUE_URL is not set")
		os.Exit(1)
	}

	// Initialize S3
	s3Client, err := storage.NewS3Client(context.Background())
	if err != nil {
		logger.Error(context.Background(), log, "Failed to init S3", "error", err)
		os.Exit(1)
	}

	// Metrics Server
	go func() {
		metricsPort := ":2112"
		logger.Info(context.Background(), log, "Starting Metrics Server", "port", metricsPort)
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(metricsPort, nil); err != nil {
			logger.Error(context.Background(), log, "Metrics server failed", "error", err)
		}
	}()

	// Concurrency Limiter
	maxConcurrency := 1
	if val := os.Getenv("MAX_CONCURRENT_JOBS"); val != "" {
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
					maxConcurrency = n
			} 
	}

	logger.Info(context.Background(), log, "Worker started", 
			"physical_cores", runtime.NumCPU(), 
			"configured_concurrency", maxConcurrency,
	)
	
	sem := make(chan token, maxConcurrency)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	var wg sync.WaitGroup
	backoff := time.Second

	for {
		select {
		case <-stop:
			logger.Info(context.Background(), log, "Shutting down, waiting for active jobs...")
			wg.Wait()
			return
		case sem <- token{}:
		}

		// Fetch from SQS
		msgOutput, err := sqsClient.ReceiveMessage(context.TODO(), &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 1,
			WaitTimeSeconds:     5,
			VisibilityTimeout:   960,
		})

		if err != nil {
			logger.Error(context.Background(), log, "SQS receive failed", "error", err)
			<-sem 
			time.Sleep(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		// Reset backoff on success
		backoff = time.Second

		if len(msgOutput.Messages) == 0 {
			<-sem
			continue
		}

		// Process Job
		wg.Add(1)
		activeJobs.Inc()
		go func(m types.Message) {
			defer wg.Done()
			defer activeJobs.Dec()
			defer func() { <-sem }()

			ctx := context.Background()
			carrier := propagation.MapCarrier{}
			for k, v := range m.MessageAttributes {
				if v.StringValue != nil {
					carrier[k] = *v.StringValue
				}
			}
			parentCtx := otel.GetTextMapPropagator().Extract(ctx, carrier)
			tracer := otel.Tracer("worker")
			ctx, span := tracer.Start(parentCtx, "process_job",
				trace.WithAttributes(attribute.String("sqs.message_id", *m.MessageId)))
			defer span.End()

			var job Job
			if err := json.Unmarshal([]byte(*m.Body), &job); err != nil {
				logger.Error(ctx, log, "Invalid job format", "body", *m.Body)
				deleteMessage(ctx, sqsClient, queueURL, m.ReceiptHandle, log)
				return
			}

			span.SetAttributes(attribute.String("job.file_id", job.FileID))
			logger.Info(ctx, log, "Processing job started", "job_id", job.FileID)

			if err := processVideoABR(ctx, s3Client, job, log); err != nil {
				logger.Error(ctx, log, "Job failed", "job_id", job.FileID, "error", err)
				// Do NOT delete message; let VisibilityTimeout expire so it retries
			} else {
				logger.Info(ctx, log, "Job complete", "job_id", job.FileID)
				deleteMessage(ctx, sqsClient, queueURL, m.ReceiptHandle, log)
			}
		}(msgOutput.Messages[0])
	}
}

func deleteMessage(ctx context.Context, client *sqs.Client, queueURL string, receiptHandle *string, log *slog.Logger) {
	_, err := client.DeleteMessage(context.TODO(), &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueURL),
		ReceiptHandle: receiptHandle,
	})
	if err != nil {
		logger.Error(ctx, log, "Failed to delete SQS message", "error", err)
	}
}

func monitorFFmpegOutput(stream io.ReadCloser, fileID string) {
	scanner := bufio.NewScanner(stream)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Parse Bitrate
		if strings.Contains(line, "bitrate=") {
			matches := reBitrate.FindStringSubmatch(line)
			if len(matches) > 1 {
				if val, err := strconv.ParseFloat(matches[1], 64); err == nil {
					currentBitrate.WithLabelValues(fileID).Set(val)
				}
			}
		}

		// Parse SSIM
		if strings.Contains(line, "Y:") {
			matches := reSSIM.FindStringSubmatch(line)
			if len(matches) > 1 {
				if val, err := strconv.ParseFloat(matches[1], 64); err == nil {
					videoQuality.WithLabelValues(fileID, "1080p").Set(val)
				}
			}
		}

		// Reduce log spam
		if strings.Contains(line, "Y:") || !strings.Contains(line, "frame=") {
			fmt.Fprintln(os.Stderr, line)
		}
	}
}

func processVideoABR(ctx context.Context, s3Client *s3.Client, job Job, log *slog.Logger) error {
	outputKey := fmt.Sprintf("%s/master.m3u8", job.FileID)
	_, err := s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(os.Getenv("PROCESSED_BUCKET")),
		Key:    aws.String(outputKey),
	})
	if err == nil {
		logger.Info(ctx, log, "Job already processed, skipping", "job_id", job.FileID)
		return nil
	}

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
	logger.Info(ctx, log, "Downloading raw video...", "key", "uploads/"+job.FileID)

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

	// ABR Transcode with 720p Quality Check
	filterComplex := "[0:v]split=3[v1][v2][v3];" +
		"[v1]scale=w=1920:h=1080[v1out];" +
		"[v2]scale=w=1280:h=720,split[v2out][v2metric];" +
		"[v3]scale=w=854:h=480[v3out];" +
		"[v2metric]scale=w=1920:h=1080[v2upscaled];" +
		"[v2upscaled][0:v]ssim=stats_file=-[ssimstats]"

	cmd := exec.CommandContext(procCtx, "ffmpeg",
		"-y",
		"-i", tempInput,
		"-filter_complex", filterComplex,
		"-map", "[ssimstats]", "-f", "null", "-",
		// Stream 1: 1080p (High)
		"-map", "[v1out]", "-c:v:0", "libx264", "-b:v:0", "4500k", "-maxrate:v:0", "5000k", "-bufsize:v:0", "7500k",
		// Stream 2: 720p (Med)
		"-map", "[v2out]", "-c:v:1", "libx264", "-b:v:1", "2500k", "-maxrate:v:1", "2750k", "-bufsize:v:1", "3750k",
		// Stream 3: 480p (Low)
		"-map", "[v3out]", "-c:v:2", "libx264", "-b:v:2", "1000k", "-maxrate:v:2", "1100k", "-bufsize:v:2", "1500k",
		// Audio (Copied to all streams) Commented since no audio in sample video
		// "-map", "a:0", "-c:a", "aac", "-b:a", "128k", "-ac", "2",
		// HLS Settings
		"-f", "hls",
		// Uncomment if audio in file and comment out second line below
		// "-var_stream_map", "v:0,a:0 v:1,a:0 v:2,a:0",
		"-var_stream_map", "v:0 v:1 v:2",
		"-master_pl_name", "master.m3u8",
		"-hls_time", "4",
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(outputDir, "%v", "segment_%03d.ts"),
		filepath.Join(outputDir, "%v", "playlist.m3u8"),
	)

	stderr, err := cmd.StderrPipe()
	stdout, err := cmd.StdoutPipe()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg failed to start: %w", err)
	}

	go monitorFFmpegOutput(stderr, job.FileID)
	go monitorFFmpegOutput(stdout, job.FileID)

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	processedBucket := os.Getenv("PROCESSED_BUCKET")
	if err := uploadDirectoryToS3(procCtx, s3Client, outputDir, processedBucket, job.FileID); err != nil {
		return fmt.Errorf("failed to upload HLS: %w", err)
	}

	transcodeDuration.Observe(time.Since(start).Seconds())
	return nil
}

func uploadDirectoryToS3(ctx context.Context, s3Client *s3.Client, localDir, bucket, s3Prefix string) error {
	return filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil { return err }
		if info.IsDir() {	return nil }

		relPath, err := filepath.Rel(localDir, path)
		key := filepath.ToSlash(filepath.Join(s3Prefix, relPath))
		file, err := os.Open(path)
		if err != nil { return err }
		defer file.Close()

		contentType := "application/octet-stream"
		switch filepath.Ext(path) {
		case ".m3u8":
			contentType = "application/vnd.apple.mpegurl"
		case ".ts":
			contentType = "video/mp2t"
		}

		_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			Body:        file,
			ContentType: aws.String(contentType),
		})
		return err
	})
}
