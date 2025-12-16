package storage

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Default timeout for s3 operations
const DefaultS3Timeout = 30 * time.Second

type Client struct {
	*s3.Client
}

func NewS3Client(ctx context.Context) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(os.Getenv("AWS_REGION")),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	s3Client := s3.NewFromConfig(cfg)

	return &Client{s3Client}, nil
}

func (c *Client) GeneratePresignedURL(ctx context.Context, bucket, key, contentType string, lifetime time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultS3Timeout)
	defer cancel()

	presignClient := s3.NewPresignClient(c.Client)

	req, err := presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
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
