package worker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.opentelemetry.io/otel/attribute"

	"github.com/amillerrr/hls-pipeline/pkg/models"
)

// Directory paths for temporary files
const (
	TempUploadDir = "/tmp/uploads"
	TempHLSDir    = "/tmp/hls"
)

// Downloader handles downloading videos from S3.
type Downloader struct {
	s3Client *s3.Client
	log      *slog.Logger
}

// NewDownloader creates a new Downloader.
func NewDownloader(s3Client *s3.Client, log *slog.Logger) *Downloader {
	return &Downloader{
		s3Client: s3Client,
		log:      log,
	}
}

// Download downloads a video from S3 to a local temporary file.
func (d *Downloader) Download(ctx context.Context, job *models.VideoJob) (string, error) {
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
	result, err := d.s3Client.GetObject(ctx, &s3.GetObjectInput{
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
	d.log.InfoContext(ctx, "Downloaded video",
		"videoId", job.VideoID,
		"sizeBytes", written,
	)

	return tmpPath, nil
}

// CreateHLSDir creates the output directory for HLS files.
func (d *Downloader) CreateHLSDir(videoID string) (string, error) {
	hlsDir := filepath.Join(TempHLSDir, videoID)
	if err := os.MkdirAll(hlsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create HLS directory: %w", err)
	}
	return hlsDir, nil
}

// Cleanup removes the temporary video file.
func (d *Downloader) Cleanup(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		d.log.Warn("Failed to remove temp file", "path", path, "error", err)
	}
}

// CleanupDir removes a directory and all its contents.
func (d *Downloader) CleanupDir(path string) {
	if err := os.RemoveAll(path); err != nil {
		d.log.Warn("Failed to remove directory", "path", path, "error", err)
	}
}
