package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/amillerrr/hls-pipeline/internal/config"
	"github.com/amillerrr/hls-pipeline/internal/metrics"
	"github.com/amillerrr/hls-pipeline/internal/storage"
	"github.com/amillerrr/hls-pipeline/internal/transcoder"
	"github.com/amillerrr/hls-pipeline/pkg/models"
)

// SQS configuration constants
const (
	SQSMaxMessages       = 1
	SQSWaitTimeSeconds   = 20
	SQSVisibilityTimeout = 900 // 15 minutes
	RetryBackoffPeriod   = 5 * time.Second
)

var tracer = otel.Tracer("hls-worker")

// Worker handles video processing jobs from SQS.
type Worker struct {
	s3Client    *s3.Client
	sqsClient   *sqs.Client
	videoRepo   *storage.VideoRepository
	transcoder  *transcoder.Transcoder
	downloader  *Downloader
	uploader    *Uploader
	cfg         *config.Config
	log         *slog.Logger
}

// Config holds worker dependencies.
type Config struct {
	S3Client   *s3.Client
	SQSClient  *sqs.Client
	VideoRepo  *storage.VideoRepository
	Transcoder *transcoder.Transcoder
	AppConfig  *config.Config
	Logger     *slog.Logger
}

// New creates a new Worker with the given configuration.
func New(cfg *Config) *Worker {
	return &Worker{
		s3Client:   cfg.S3Client,
		sqsClient:  cfg.SQSClient,
		videoRepo:  cfg.VideoRepo,
		transcoder: cfg.Transcoder,
		downloader: NewDownloader(cfg.S3Client, cfg.Logger),
		uploader:   NewUploader(cfg.S3Client, cfg.AppConfig.AWS.ProcessedBucket, cfg.Logger),
		cfg:        cfg.AppConfig,
		log:        cfg.Logger,
	}
}

// Run starts the worker and blocks until the context is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.log.InfoContext(ctx, "Starting queue polling",
		"queueURL", w.cfg.AWS.SQSQueueURL,
		"maxConcurrent", w.cfg.Worker.MaxConcurrentJobs,
	)

	sem := make(chan struct{}, w.cfg.Worker.MaxConcurrentJobs)
	var wg sync.WaitGroup

messageLoop:
	for {
		select {
		case <-ctx.Done():
			w.log.InfoContext(ctx, "Waiting for in-progress jobs to complete...")
			wg.Wait()
			w.log.InfoContext(ctx, "All jobs completed, shutting down")
			return
		default:
		}

		// Receive messages
		result, err := w.sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(w.cfg.AWS.SQSQueueURL),
			MaxNumberOfMessages: SQSMaxMessages,
			WaitTimeSeconds:     SQSWaitTimeSeconds,
			VisibilityTimeout:   SQSVisibilityTimeout,
		})
		if err != nil {
			if ctx.Err() != nil {
				continue // Shutting down
			}
			w.log.ErrorContext(ctx, "Failed to receive messages", "error", err)
			time.Sleep(RetryBackoffPeriod)
			continue
		}

		for _, msg := range result.Messages {
			select {
			case sem <- struct{}{}:
				wg.Add(1)
				go func(msg types.Message) {
					defer wg.Done()
					defer func() { <-sem }()

					metrics.ActiveJobs.Inc()
					defer metrics.ActiveJobs.Dec()

					if err := w.processMessage(ctx, msg); err != nil {
						w.log.ErrorContext(ctx, "Failed to process message",
							"error", err,
							"messageId", safeStringDeref(msg.MessageId),
						)
						metrics.RecordFailure()
					} else {
						// Delete message on success
						_, delErr := w.sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
							QueueUrl:      aws.String(w.cfg.AWS.SQSQueueURL),
							ReceiptHandle: msg.ReceiptHandle,
						})
						if delErr != nil {
							w.log.ErrorContext(ctx, "Failed to delete message", "error", delErr)
						}
						metrics.RecordSuccess()
					}
				}(msg)
			case <-ctx.Done():
				w.log.InfoContext(ctx, "Context cancelled, stopping message processing")
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
		return fmt.Errorf("%w: empty message body", models.ErrJobParseFailed)
	}

	var job models.VideoJob
	if err := json.Unmarshal([]byte(*msg.Body), &job); err != nil {
		return fmt.Errorf("%w: %v", models.ErrJobParseFailed, err)
	}

	if err := job.Validate(); err != nil {
		return fmt.Errorf("%w: %v", models.ErrJobParseFailed, err)
	}

	span.SetAttributes(
		attribute.String("video.id", job.VideoID),
		attribute.String("video.s3_key", job.S3Key),
		attribute.String("video.filename", job.Filename),
	)

	return w.processVideo(ctx, &job)
}

func (w *Worker) processVideo(ctx context.Context, job *models.VideoJob) error {
	w.log.InfoContext(ctx, "Processing video",
		"videoId", job.VideoID,
		"s3Key", job.S3Key,
		"filename", job.Filename,
	)

	// Update status to processing
	if err := w.videoRepo.UpdateVideoProcessing(ctx, job.VideoID); err != nil {
		w.log.WarnContext(ctx, "Failed to update video status to processing",
			"videoId", job.VideoID,
			"error", err,
		)
	}

	// Track processing error for deferred failure handling
	var processingErr error
	defer func() {
		if processingErr != nil {
			if failErr := w.videoRepo.FailVideoProcessing(ctx, job.VideoID, processingErr.Error()); failErr != nil {
				w.log.ErrorContext(ctx, "Failed to mark video as failed",
					"videoId", job.VideoID,
					"error", failErr,
				)
			}
		}
	}()

	start := time.Now()

	// Download video from S3
	downloadStart := time.Now()
	localPath, err := w.downloader.Download(ctx, job)
	if err != nil {
		processingErr = fmt.Errorf("%w: %v", models.ErrDownloadFailed, err)
		return processingErr
	}
	metrics.DownloadDuration.Observe(time.Since(downloadStart).Seconds())
	defer w.downloader.Cleanup(localPath)

	// Check for context cancellation before transcoding
	if ctx.Err() != nil {
		processingErr = fmt.Errorf("%w: before transcoding", models.ErrContextCanceled)
		return processingErr
	}

	// Create HLS output directory
	hlsDir, err := w.downloader.CreateHLSDir(job.VideoID)
	if err != nil {
		processingErr = fmt.Errorf("%w: %v", models.ErrTranscodeFailed, err)
		return processingErr
	}
	defer w.downloader.CleanupDir(hlsDir)

	// Create output directories for each quality level
	if err := transcoder.CreateOutputDirectories(hlsDir, w.transcoder.GetPresets()); err != nil {
		processingErr = fmt.Errorf("%w: %v", models.ErrTranscodeFailed, err)
		return processingErr
	}

	// Transcode to HLS
	if err := w.transcoder.TranscodeToHLS(ctx, job.VideoID, localPath, hlsDir); err != nil {
		processingErr = fmt.Errorf("%w: %v", models.ErrTranscodeFailed, err)
		return processingErr
	}

	// Calculate quality metrics (non-blocking)
	w.transcoder.CalculateQualityMetrics(ctx, localPath, hlsDir)

	// Check for context cancellation before uploading
	if ctx.Err() != nil {
		processingErr = fmt.Errorf("%w: before upload", models.ErrContextCanceled)
		return processingErr
	}

	// Upload HLS files to S3
	uploadStart := time.Now()
	if err := w.uploader.Upload(ctx, job.VideoID, hlsDir); err != nil {
		processingErr = fmt.Errorf("%w: %v", models.ErrUploadFailed, err)
		return processingErr
	}
	metrics.UploadDuration.Observe(time.Since(uploadStart).Seconds())

	// Record total processing duration
	duration := time.Since(start).Seconds()
	metrics.ProcessingDuration.WithLabelValues("all").Observe(duration)

	// Update DynamoDB with completion info
	hlsPrefix := fmt.Sprintf("hls/%s/", job.VideoID)
	playbackURL := fmt.Sprintf("https://%s/hls/%s/master.m3u8", w.cfg.AWS.CDNDomain, job.VideoID)

	modelPresets := transcoder.ToModelPresets(w.transcoder.GetPresets())
	if err := w.videoRepo.CompleteVideoProcessing(ctx, job.VideoID, playbackURL, hlsPrefix, modelPresets); err != nil {
		w.log.ErrorContext(ctx, "Failed to mark video as completed in DynamoDB",
			"videoId", job.VideoID,
			"error", err,
		)
		// Don't set processingErr here - the video was processed successfully
	}

	w.log.InfoContext(ctx, "Video processed successfully",
		"videoId", job.VideoID,
		"filename", job.Filename,
		"durationSeconds", duration,
		"playbackURL", playbackURL,
	)

	return nil
}
