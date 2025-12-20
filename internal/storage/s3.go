package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"

	"github.com/amillerrr/hls-pipeline/internal/config"
)

// Default timeout for S3 operations
const DefaultS3Timeout = 30 * time.Second

// S3Client wraps the AWS S3 client with additional functionality.
type S3Client struct {
	*s3.Client
	presignClient *s3.PresignClient
}

// NewS3Client creates a new S3 client using the provided configuration.
func NewS3Client(ctx context.Context, cfg *config.Config) (*S3Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.AWS.Region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Add OpenTelemetry instrumentation
	otelaws.AppendMiddlewares(&awsCfg.APIOptions)

	client := s3.NewFromConfig(awsCfg)

	return &S3Client{
		Client:        client,
		presignClient: s3.NewPresignClient(client),
	}, nil
}

// NewS3ClientFromAWSConfig creates a new S3 client from an existing AWS config.
func NewS3ClientFromAWSConfig(awsCfg aws.Config) *S3Client {
	client := s3.NewFromConfig(awsCfg)
	return &S3Client{
		Client:        client,
		presignClient: s3.NewPresignClient(client),
	}
}

// GeneratePresignedURL generates a presigned URL for uploading an object.
func (c *S3Client) GeneratePresignedURL(ctx context.Context, bucket, key, contentType string, lifetime time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultS3Timeout)
	defer cancel()

	req, err := c.presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = lifetime
	})

	if err != nil {
		return "", fmt.Errorf("failed to presign request: %w", err)
	}

	return req.URL, nil
}

// ObjectExists checks if an object exists in S3.
func (c *S3Client) ObjectExists(ctx context.Context, bucket, key string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultS3Timeout)
	defer cancel()

	_, err := c.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		// Check if it's a "not found" error
		// Note: In production, you'd want to check for specific error types
		return false, nil
	}

	return true, nil
}

// GetObjectSize returns the size of an object in bytes.
func (c *S3Client) GetObjectSize(ctx context.Context, bucket, key string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultS3Timeout)
	defer cancel()

	result, err := c.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return 0, fmt.Errorf("failed to get object metadata: %w", err)
	}

	if result.ContentLength != nil {
		return *result.ContentLength, nil
	}

	return 0, nil
}
