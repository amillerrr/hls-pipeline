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

	"github.com/amillerrr/hls-pipeline/internal/config"
	"github.com/amillerrr/hls-pipeline/internal/drm"
	"github.com/amillerrr/hls-pipeline/internal/storage"
	"github.com/amillerrr/hls-pipeline/internal/transcoder"
	"github.com/amillerrr/hls-pipeline/pkg/models"
)

// Worker processes transcoding jobs from SQS.
type Worker struct {
	config    *config.Config
	logger    *slog.Logger
	sqsClient *sqs.Client

	// Components
	pipeline         transcoder.PipelineInterface
	downloader       *Downloader
	uploader         *Uploader
	repo             *storage.VideoRepository
	s3EventProcessor *S3EventProcessor

	// Concurrency control
	semaphore chan struct{}
	wg        sync.WaitGroup

	// Shutdown
	shutdown chan struct{}
	running  bool
	mu       sync.Mutex
}

// NewWorker creates a new worker instance.
func NewWorker(cfg *config.Config, sqsC *sqs.Client, s3C *s3.Client, ddbC *dynamodb.Client, log *slog.Logger) (*Worker, error) {
	// Initialize DRM Provider if enabled
	var keyProvider drm.KeyProvider
	var err error

	if cfg.DRM.Enabled {
		drmCfg := &drm.ProviderConfig{
			WidevineEnabled:     cfg.DRM.WidevineLicenseURL != "",
			WidevineLicenseURL:  cfg.DRM.WidevineLicenseURL,
			FairPlayEnabled:     cfg.DRM.FairPlayLicenseURL != "",
			FairPlayLicenseURL:  cfg.DRM.FairPlayLicenseURL,
			FairPlayCertificate: cfg.DRM.FairPlayCertURL,
			PlayReadyEnabled:    cfg.DRM.PlayReadyLicenseURL != "",
			PlayReadyLicenseURL: cfg.DRM.PlayReadyLicenseURL,
			Logger:              log,
		}

		keyProvider, err = drm.NewMultiSystemProvider(drmCfg)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize DRM provider: %w", err)
		}
	}

	// Initialize Unified Pipeline
	pipe, err := transcoder.NewPipeline(&cfg.Transcoding, log, keyProvider)
	if err != nil {
		return nil, err
	}

	// Initialize Storage Repository
	repo := storage.NewVideoRepositoryFromClient(ddbC, cfg.AWS.DynamoDBTable)

	// Initialize S3 Event Processor
	s3Processor := NewS3EventProcessor(DefaultS3EventFilter(), log)

	maxJobs := cfg.Worker.MaxConcurrentJobs
	if maxJobs <= 0 {
		maxJobs = 2
	}

	return &Worker{
		config:           cfg,
		logger:           log,
		sqsClient:        sqsC,
		pipeline:         pipe,
		downloader:       NewDownloader(s3C, log),
		uploader:         NewUploader(s3C, cfg.AWS.ProcessedBucket, log),
		repo:             repo,
		s3EventProcessor: s3Processor,
		semaphore:        make(chan struct{}, maxJobs),
		shutdown:         make(chan struct{}),
	}, nil
}

// Start starts the worker polling loop.
func (w *Worker) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return fmt.Errorf("worker already running")
	}
	w.running = true
	w.mu.Unlock()

	w.logger.Info("worker starting",
		"queueUrl", w.config.AWS.SQSQueueURL,
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

	if !w.running {
		return
	}

	close(w.shutdown)
}

// gracefulShutdown waits for active jobs to complete.
func (w *Worker) gracefulShutdown() error {
	w.logger.Info("shutting down worker, waiting for jobs to complete")
	w.wg.Wait()
	w.logger.Info("worker shutdown complete")
	return nil
}

// pollMessages polls for new messages from SQS.
func (w *Worker) pollMessages(ctx context.Context) error {
	output, err := w.sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(w.config.AWS.SQSQueueURL),
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     20, // Long polling
		VisibilityTimeout:   900, // 15 minutes
		AttributeNames:      []types.QueueAttributeName{types.QueueAttributeNameAll},
	})
	if err != nil {
		return fmt.Errorf("failed to receive messages: %w", err)
	}

	for _, msg := range output.Messages {
		// Try to acquire semaphore (limit concurrent jobs)
		select {
		case w.semaphore <- struct{}{}:
			w.wg.Add(1)
			go w.processMessage(ctx, msg)
		default:
			// All workers busy, simple backoff or just don't pick up yet.
			// In a real system we might not receive messages if we can't process them,
			// but here we just wait for the next poll cycle.
			w.logger.Debug("all workers busy")
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

	// Nil job means filtered out (not an error, e.g. wrong prefix)
	if job == nil {
		w.logger.Debug("message filtered out",
			"messageId", aws.ToString(msg.MessageId),
		)
		w.deleteMessage(ctx, msg)
		return
	}

	w.logger.Info("processing job",
		"videoId", job.VideoID,
		"bucket", job.Bucket,
		"key", job.Key,
	)

	// 1. Ensure Video Record Exists & Mark Processing
	// If triggered by S3 event, the record might not exist yet if API didn't create it
	// or if it was a direct S3 upload.
	_, err = w.repo.GetVideo(ctx, job.VideoID)
	if err != nil {
		// Create placeholder if missing
		_, err = w.repo.CreateVideo(ctx, job.VideoID, job.Key, job.Key, job.Size)
		if err != nil {
			w.logger.Warn("failed to create video record (might exist)", "error", err)
		}
	}

	if err := w.repo.UpdateVideoProcessing(ctx, job.VideoID); err != nil {
		w.logger.Warn("failed to update video status to processing", "error", err)
	}

	// 2. Execute Transcoding Pipeline
	if err := w.processJob(ctx, job); err != nil {
		w.logger.Error("job processing failed",
			"videoId", job.VideoID,
			"error", err,
		)

		// Update DB with failure
		_ = w.repo.FailVideoProcessing(ctx, job.VideoID, err.Error())
		return
	}

	// 3. Success - delete the message
	w.deleteMessage(ctx, msg)

	w.logger.Info("job completed successfully",
		"videoId", job.VideoID,
	)
}

// processJob executes the actual media processing pipeline.
func (w *Worker) processJob(ctx context.Context, job *JobMessage) error {
	// 1. Download Source
	w.logger.Debug("downloading source file", "videoId", job.VideoID)
	localInputPath, err := w.downloader.Download(ctx, &models.VideoJob{
		VideoID: job.VideoID,
		Bucket:  job.Bucket,
		S3Key:   job.Key,
	})
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer w.downloader.Cleanup(localInputPath)

	// 2. Prepare Output Directory
	localOutputDir, err := w.downloader.CreateHLSDir(job.VideoID)
	if err != nil {
		return fmt.Errorf("create output dir failed: %w", err)
	}
	defer w.downloader.CleanupDir(localOutputDir)

	// 3. Transcode & Package (Unified)
	// Check if options explicitly enable DRM, or fall back to global config
	enableDRM := w.config.DRM.Enabled
	if job.Options != nil && job.Options.EnableDRM {
		enableDRM = true
	}

	w.logger.Debug("starting unified pipeline", "videoId", job.VideoID, "drm", enableDRM)
	err = w.pipeline.Process(ctx, transcoder.JobInput{
		InputPath:  localInputPath,
		OutputDir:  localOutputDir,
		StreamID:   job.VideoID,
		Presets:    w.config.Transcoding.Presets,
		EnableDRM:  enableDRM,
	})
	if err != nil {
		return fmt.Errorf("transcoding failed: %w", err)
	}

	// 4. Upload Result (Manifests + Segments)
	w.logger.Debug("uploading processed files", "videoId", job.VideoID)
	if err := w.uploader.Upload(ctx, job.VideoID, localOutputDir); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	// 5. Update Metadata
	hlsUrl := fmt.Sprintf("https://%s/processed/%s/master.m3u8", w.config.AWS.CDNDomain, job.VideoID)
	
	// We could also add the DASH URL if needed:
	// dashUrl := fmt.Sprintf("https://%s/processed/%s/manifest.mpd", w.config.AWS.CDNDomain, job.VideoID)

	// Convert config presets to models.QualityPreset if needed by repo, 
	// or pass nil if the repo function signature allows.
	// For now passing nil for presets list to simplify.
	return w.repo.CompleteVideoProcessing(ctx, job.VideoID, hlsUrl, "", nil)
}

// deleteMessage deletes a processed message from SQS.
func (w *Worker) deleteMessage(ctx context.Context, msg types.Message) {
	_, err := w.sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(w.config.AWS.SQSQueueURL),
		ReceiptHandle: msg.ReceiptHandle,
	})
	if err != nil {
		w.logger.Warn("failed to delete message",
			"messageId", aws.ToString(msg.MessageId),
			"error", err,
		)
	}
}

// IsTestEvent checks if the message is an S3 test event.
func IsTestEvent(body string) bool {
	return body == "s3:TestEvent" ||
		(len(body) > 0 && body[0] == '{' &&
			(contains(body, `"Event":"s3:TestEvent"`) ||
				contains(body, `"event":"s3:TestEvent"`)))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(s) > len(substr) &&
				(s[:len(substr)] == substr ||
					s[len(s)-len(substr):] == substr ||
					containsInner(s, substr)))
}

func containsInner(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
