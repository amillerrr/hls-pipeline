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

	endpoint := os.Getenv("S3_ENDPOINT")
	var s3Client *s3.Client

	if endpoint != "" {
		s3Client = s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true // Required for MinIO/LocalStack
		})
	} else {
		s3Client = s3.NewFromConfig(cfg)
	}

	return &Client{s3Client}, nil
}

func (c *Client) GeneratePresignedURL(ctx context.Context, bucket, key, contentType string, lifetime time.Duration) (string, error) {
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
