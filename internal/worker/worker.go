package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/amillerrr/hls-pipeline/internal/config"
	"github.com/amillerrr/hls-pipeline/internal/storage"
	"github.com/amillerrr/hls-pipeline/internal/transcoder"
	"github.com/amillerrr/hls-pipeline/pkg/models"
)

type Worker struct {
	config     *config.Config
	logger     *slog.Logger
	sqs        *sqs.Client
	pipeline   *transcoder.Pipeline
	downloader *Downloader
	uploader   *Uploader
	repo       *storage.VideoRepository
}

func NewWorker(cfg *config.Config, sqsC *sqs.Client, s3C *s3.Client, ddbC *dynamodb.Client, log *slog.Logger) (*Worker, error) {
	// Initialize Unified Pipeline
	pipe, err := transcoder.NewPipeline(&cfg.Transcoding, log)
	if err != nil {
		return nil, err
	}

	return &Worker{
		config:     cfg,
		logger:     log,
		sqs:        sqsC,
		pipeline:   pipe,
		downloader: NewDownloader(s3C, log),
		uploader:   NewUploader(s3C, cfg.AWS.ProcessedBucket, log),
		repo:       storage.NewVideoRepositoryFromClient(ddbC, cfg.AWS.DynamoDBTable),
	}, nil
}

// Start (same logic as before, just calling processMessage) ...

func (w *Worker) processJob(ctx context.Context, job *JobMessage) error {
	w.logger.Info("processing job", "videoId", job.VideoID)

	// 1. Download Source
	localInputPath, err := w.downloader.Download(ctx, &models.VideoJob{
		VideoID: job.VideoID,
		Bucket:  job.Bucket,
		S3Key:   job.Key,
	})
	if err != nil {
		return err
	}
	defer w.downloader.Cleanup(localInputPath)

	// 2. Prepare Output Directory
	localOutputDir, err := w.downloader.CreateHLSDir(job.VideoID)
	if err != nil {
		return err
	}
	defer w.downloader.CleanupDir(localOutputDir)

	// 3. Transcode & Package (Unified)
	err = w.pipeline.Process(ctx, transcoder.JobInput{
		InputPath: localInputPath,
		OutputDir: localOutputDir,
		StreamID:  job.VideoID,
		Presets:   w.config.Transcoding.Presets,
	})
	if err != nil {
		return fmt.Errorf("transcoding failed: %w", err)
	}

	// 4. Upload Result (Manifests + Segments)
	if err := w.uploader.Upload(ctx, job.VideoID, localOutputDir); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	// 5. Update Metadata
	hlsUrl := fmt.Sprintf("https://%s/processed/%s/master.m3u8", w.config.AWS.CDNDomain, job.VideoID)
	return w.repo.CompleteVideoProcessing(ctx, job.VideoID, hlsUrl, "", nil)
}
