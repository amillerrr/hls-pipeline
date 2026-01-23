package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"

	"github.com/amillerrr/hls-pipeline/internal/config"
	"github.com/amillerrr/hls-pipeline/internal/metrics"
	"github.com/amillerrr/hls-pipeline/internal/transcoder"
	"github.com/amillerrr/hls-pipeline/pkg/models"
)

// Worker processes transcoding jobs from SQS.
type Worker struct {
	config         *config.Config
	sqsClient      *sqs.Client
	s3Client       *s3.Client
	dynamoDBClient *dynamodb.Client
	logger         *slog.Logger

	// Processing
	transcoder       *transcoder.LLHLSTranscoder
	s3EventProcessor *S3EventProcessor

	// Concurrency control
	semaphore chan struct{}
	wg        sync.WaitGroup

	// Shutdown
	shutdown chan struct{}
	running  bool
	mu       sync.Mutex
}

// WorkerConfig contains worker configuration.
type WorkerConfig struct {
	QueueURL          string
	MaxConcurrentJobs int
	VisibilityTimeout int32
	WaitTimeSeconds   int32
	ProcessedBucket   string
	TableName         string
}

// NewWorker creates a new worker instance.
func NewWorker(
	cfg *config.Config,
	sqsClient *sqs.Client,
	s3Client *s3.Client,
	dynamoDBClient *dynamodb.Client,
	logger *slog.Logger,
) (*Worker, error) {
	// Create transcoder
	tc, err := transcoder.NewLLHLSTranscoder(&transcoder.LLHLSConfig{
		SegmentDuration: cfg.SegmentDuration,
		PartDuration:    cfg.PartDuration,
		EnableCMAF:      cfg.EnableCMAF,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create transcoder: %w", err)
	}

	// Create S3 event processor with default filter
	s3EventProcessor := NewS3EventProcessor(DefaultS3EventFilter(), logger)

	maxJobs := cfg.MaxConcurrentJobs
	if maxJobs <= 0 {
		maxJobs = 2
	}

	return &Worker{
		config:           cfg,
		sqsClient:        sqsClient,
		s3Client:         s3Client,
		dynamoDBClient:   dynamoDBClient,
		logger:           logger,
		transcoder:       tc,
		s3EventProcessor: s3EventProcessor,
		semaphore:        make(chan struct{}, maxJobs),
		shutdown:         make(chan struct{}),
	}, nil
}

// Start starts the worker.
func (w *Worker) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return fmt.Errorf("worker already running")
	}
	w.running = true
	w.mu.Unlock()

	w.logger.Info("worker starting",
		"queueUrl", w.config.SQSQueueURL,
		"maxConcurrentJobs", cap(w.semaphore),
	)

	// Main polling loop
	for {
		select {
		case <-ctx.Done():
			return w.gracefulShutdown()
		case <-w.shutdown:
			return w.gracefulShutdown()
		default:
			if err := w.pollMessages(ctx); err != nil {
				w.logger.Error("error polling messages", "error", err)
				time.Sleep(5 * time.Second)
			}
		}
	}
}

// Stop stops the worker gracefully.
func (w *Worker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		close(w.shutdown)
		w.running = false
	}
}

// gracefulShutdown waits for in-flight jobs to complete.
func (w *Worker) gracefulShutdown() error {
	w.logger.Info("worker shutting down, waiting for in-flight jobs...")

	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		w.logger.Info("worker shutdown complete")
	case <-time.After(5 * time.Minute):
		w.logger.Warn("worker shutdown timeout, some jobs may not have completed")
	}

	return nil
}

// pollMessages polls SQS for messages.
func (w *Worker) pollMessages(ctx context.Context) error {
	result, err := w.sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:              aws.String(w.config.SQSQueueURL),
		MaxNumberOfMessages:   10,
		WaitTimeSeconds:       20,
		VisibilityTimeout:     3600, // 1 hour
		MessageAttributeNames: []string{"All"},
	})
	if err != nil {
		return fmt.Errorf("failed to receive messages: %w", err)
	}

	for _, msg := range result.Messages {
		// Acquire semaphore slot
		select {
		case w.semaphore <- struct{}{}:
			w.wg.Add(1)
			go w.processMessage(ctx, msg)
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// processMessage processes a single SQS message.
func (w *Worker) processMessage(ctx context.Context, msg types.Message) {
	defer func() {
		<-w.semaphore
		w.wg.Done()
	}()

	startTime := time.Now()
	messageBody := aws.ToString(msg.Body)

	// Skip S3 test events
	if IsTestEvent(messageBody) {
		w.logger.Debug("skipping S3 test event")
		w.deleteMessage(ctx, msg)
		return
	}

	// Parse the message - handles both S3 events and direct API calls
	job, err := w.s3EventProcessor.ProcessMessage(ctx, messageBody)
	if err != nil {
		w.logger.Error("failed to parse message",
			"error", err,
			"messageId", aws.ToString(msg.MessageId),
		)
		// Don't delete - let it go to DLQ after max retries
		return
	}

	// Nil job means filtered out (not an error)
	if job == nil {
		w.logger.Debug("message filtered out",
			"messageId", aws.ToString(msg.MessageId),
		)
		w.deleteMessage(ctx, msg)
		return
	}

	w.logger.Info("processing job",
		"videoId", job.VideoID,
		"source", job.Source,
		"bucket", job.Bucket,
		"key", job.Key,
	)

	// Create or update video record in DynamoDB
	video, err := w.getOrCreateVideo(ctx, job)
	if err != nil {
		w.logger.Error("failed to get/create video record",
			"videoId", job.VideoID,
			"error", err,
		)
		return
	}

	// Update status to processing
	video.SetStatus(models.StatusProcessing)
	if err := w.updateVideo(ctx, video); err != nil {
		w.logger.Warn("failed to update video status", "error", err)
	}

	// Process the job
	if err := w.processJob(ctx, job, video); err != nil {
		w.logger.Error("job processing failed",
			"videoId", job.VideoID,
			"error", err,
			"duration", time.Since(startTime),
		)

		video.SetError(models.ErrCodeTranscodeFailed, err.Error())
		w.updateVideo(ctx, video)

		metrics.RecordTranscodeError("transcode_failed", "processing")
		return
	}

	// Success - delete the message
	w.deleteMessage(ctx, msg)

	w.logger.Info("job completed",
		"videoId", job.VideoID,
		"duration", time.Since(startTime),
	)

	metrics.RecordTranscodeJob("success", "", time.Since(startTime).Seconds(), job.Size, 0)
}

// processJob processes a transcoding job.
func (w *Worker) processJob(ctx context.Context, job *JobMessage, video *models.Video) error {
	// Update status to transcoding
	video.SetStatus(models.StatusTranscoding)
	w.updateVideo(ctx, video)

	// Build transcode input
	input := &transcoder.TranscodeInput{
		VideoID:      job.VideoID,
		SourceBucket: job.Bucket,
		SourceKey:    job.Key,
		OutputBucket: w.config.ProcessedBucket,
		OutputPrefix: fmt.Sprintf("processed/%s", job.VideoID),
	}

	// Apply job options if present
	if job.Options != nil {
		if len(job.Options.Presets) > 0 {
			input.Presets = job.Options.Presets
		}
		if job.Options.OutputPrefix != "" {
			input.OutputPrefix = job.Options.OutputPrefix
		}
	}

	// Run transcoding
	output, err := w.transcoder.Transcode(ctx, input)
	if err != nil {
		return fmt.Errorf("transcoding failed: %w", err)
	}

	// Update video with results
	video.SetStatus(models.StatusCompleted)
	video.HLSManifestURL = fmt.Sprintf("https://%s/%s/master.m3u8",
		w.config.CDNDomain,
		input.OutputPrefix,
	)
	video.Duration = output.Duration
	video.Width = output.Width
	video.Height = output.Height

	// Add output variants
	video.Outputs = &models.VideoOutputs{
		Variants: make([]models.VariantOutput, len(output.Variants)),
	}
	for i, v := range output.Variants {
		video.Outputs.Variants[i] = models.VariantOutput{
			Name:      v.Name,
			Width:     v.Width,
			Height:    v.Height,
			Bandwidth: v.Bandwidth,
			Codecs:    v.Codecs,
		}
	}

	if err := w.updateVideo(ctx, video); err != nil {
		return fmt.Errorf("failed to update video record: %w", err)
	}

	// Send webhook if configured
	if job.Options != nil && job.Options.CallbackURL != "" {
		w.sendWebhook(ctx, job.Options.CallbackURL, video)
	}

	return nil
}

// getOrCreateVideo gets an existing video or creates a new one.
func (w *Worker) getOrCreateVideo(ctx context.Context, job *JobMessage) (*models.Video, error) {
	videoID := job.VideoID
	if videoID == "" {
		videoID = uuid.New().String()[:8]
	}

	// Try to get existing video
	// (In a real implementation, this would query DynamoDB)

	// Create new video if not found
	video := models.NewVideo(videoID)
	video.SourceKey = job.Key
	video.FileSize = job.Size

	// Copy metadata
	if job.Metadata != nil {
		video.Metadata = job.Metadata
	}

	return video, nil
}

// updateVideo updates the video record in DynamoDB.
func (w *Worker) updateVideo(ctx context.Context, video *models.Video) error {
	// In a real implementation, this would update DynamoDB
	video.UpdatedAt = time.Now()
	return nil
}

// deleteMessage deletes a processed message from SQS.
func (w *Worker) deleteMessage(ctx context.Context, msg types.Message) {
	_, err := w.sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(w.config.SQSQueueURL),
		ReceiptHandle: msg.ReceiptHandle,
	})
	if err != nil {
		w.logger.Warn("failed to delete message",
			"messageId", aws.ToString(msg.MessageId),
			"error", err,
		)
	}
}

// sendWebhook sends a webhook notification.
func (w *Worker) sendWebhook(ctx context.Context, url string, video *models.Video) {
	payload := models.WebhookPayload{
		Event:     "video.completed",
		VideoID:   video.ID,
		Status:    string(video.Status),
		Timestamp: time.Now(),
		Video:     video,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		w.logger.Warn("failed to marshal webhook payload", "error", err)
		return
	}

	// Fire and forget (in production, use a queue for reliability)
	go func() {
		// HTTP POST to webhook URL
		_ = data // Would send this to the URL
		w.logger.Debug("webhook sent", "url", url, "videoId", video.ID)
	}()
}
