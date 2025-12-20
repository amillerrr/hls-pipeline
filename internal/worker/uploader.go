package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.opentelemetry.io/otel/attribute"

	"github.com/amillerrr/hls-pipeline/pkg/models"
)

// Upload configuration
const (
	MaxConcurrentUploads = 20
)

// Uploader handles uploading HLS files to S3.
type Uploader struct {
	s3Client *s3.Client
	bucket   string
	log      *slog.Logger
}

// NewUploader creates a new Uploader.
func NewUploader(s3Client *s3.Client, bucket string, log *slog.Logger) *Uploader {
	return &Uploader{
		s3Client: s3Client,
		bucket:   bucket,
		log:      log,
	}
}

// Upload uploads all HLS files to S3.
func (u *Uploader) Upload(ctx context.Context, videoID, hlsDir string) error {
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

		// Skip temporary files (like SSIM frames)
		if strings.HasSuffix(path, ".png") {
			return nil
		}

		// Check for previous errors
		if firstErr.Load() != nil {
			return nil
		}

		// Acquire semaphore
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return fmt.Errorf("%w: during upload walk", models.ErrContextCanceled)
		}

		wg.Add(1)

		go func(filePath string, fileInfo os.FileInfo) {
			defer wg.Done()
			defer func() { <-sem }()

			// Check if previous error occurred
			if firstErr.Load() != nil {
				return
			}

			// Calculate S3 key
			relPath, err := filepath.Rel(hlsDir, filePath)
			if err != nil {
				wrappedErr := fmt.Errorf("failed to get relative path: %w", err)
				firstErr.CompareAndSwap(nil, &wrappedErr)
				return
			}
			s3Key := fmt.Sprintf("hls/%s/%s", videoID, relPath)

			// Open file
			file, err := os.Open(filePath)
			if err != nil {
				wrappedErr := fmt.Errorf("failed to open file %s: %w", filePath, err)
				firstErr.CompareAndSwap(nil, &wrappedErr)
				return
			}
			defer file.Close()

			// Determine content type
			contentType := u.getContentType(filePath)

			// Upload to S3
			_, err = u.s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket:      aws.String(u.bucket),
				Key:         aws.String(s3Key),
				Body:        file,
				ContentType: aws.String(contentType),
			})
			if err != nil {
				wrappedErr := fmt.Errorf("failed to upload %s: %w", s3Key, err)
				firstErr.CompareAndSwap(nil, &wrappedErr)
				return
			}

			// Update metrics atomically
			filesUploaded.Add(1)
			totalBytes.Add(fileInfo.Size())

			u.log.DebugContext(ctx, "Uploaded file", "key", s3Key)

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

	u.log.InfoContext(ctx, "HLS upload complete",
		"videoId", videoID,
		"filesUploaded", uploaded,
		"totalBytes", bytes,
	)

	return nil
}

// getContentType returns the appropriate content type for the file.
func (u *Uploader) getContentType(filePath string) string {
	switch {
	case strings.HasSuffix(filePath, ".m3u8"):
		return "application/vnd.apple.mpegurl"
	case strings.HasSuffix(filePath, ".ts"):
		return "video/MP2T"
	default:
		return "application/octet-stream"
	}
}
