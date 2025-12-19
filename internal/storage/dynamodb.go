package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var (
	ErrVideoNotFound = errors.New("video not found")
	ErrInvalidStatus = errors.New("invalid video status")
)

type VideoStatus string

const (
	StatusPending    VideoStatus = "pending"
	StatusProcessing VideoStatus = "processing"
	StatusCompleted  VideoStatus = "completed"
	StatusFailed     VideoStatus = "failed"
)

type QualityPreset struct {
	Name      string `dynamodbav:"name"`
	Width     int    `dynamodbav:"width"`
	Height    int    `dynamodbav:"height"`
	Bitrate   int    `dynamodbav:"bitrate"`
}

type VideoMetadata struct {
	// Keys
	PK     string `dynamodbav:"pk"`
	SK     string `dynamodbav:"sk"`
	GSI1PK string `dynamodbav:"gsi1pk,omitempty"`
	GSI1SK string `dynamodbav:"gsi1sk,omitempty"`

	// Attributes
	VideoID        string          `dynamodbav:"video_id"`
	Filename       string          `dynamodbav:"filename"`
	Status         VideoStatus     `dynamodbav:"status"`
	S3RawKey       string          `dynamodbav:"s3_raw_key"`
	S3HLSPrefix    string          `dynamodbav:"s3_hls_prefix,omitempty"`
	PlaybackURL    string          `dynamodbav:"playback_url,omitempty"`
	FileSizeBytes  int64           `dynamodbav:"file_size_bytes,omitempty"`
	DurationSeconds float64        `dynamodbav:"duration_seconds,omitempty"`
	CreatedAt      string          `dynamodbav:"created_at"`
	UpdatedAt      string          `dynamodbav:"updated_at"`
	ProcessedAt    string          `dynamodbav:"processed_at,omitempty"`
	QualityPresets []QualityPreset `dynamodbav:"quality_presets,omitempty"`
	ErrorMessage   string          `dynamodbav:"error_message,omitempty"`
}

type VideoRepository struct {
	client    *dynamodb.Client
	tableName string
}

func NewVideoRepository(ctx context.Context) (*VideoRepository, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(os.Getenv("AWS_REGION")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	tableName := os.Getenv("DYNAMODB_TABLE")
	if tableName == "" {
		return nil, errors.New("DYNAMODB_TABLE environment variable not set")
	}

	return &VideoRepository{
		client:    dynamodb.NewFromConfig(cfg),
		tableName: tableName,
	}, nil
}

// CreateVideo creates a new video metadata record
func (r *VideoRepository) CreateVideo(ctx context.Context, videoID, filename, s3RawKey string, fileSizeBytes int64) (*VideoMetadata, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	
	video := &VideoMetadata{
		PK:            fmt.Sprintf("VIDEO#%s", videoID),
		SK:            "METADATA",
		GSI1PK:        "ALL_VIDEOS",
		GSI1SK:        fmt.Sprintf("%s#%s", now, videoID),
		VideoID:       videoID,
		Filename:      filename,
		Status:        StatusPending,
		S3RawKey:      s3RawKey,
		FileSizeBytes: fileSizeBytes,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	item, err := attributevalue.MarshalMap(video)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal video: %w", err)
	}

	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(r.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return nil, fmt.Errorf("video already exists: %s", videoID)
		}
		return nil, fmt.Errorf("failed to create video: %w", err)
	}

	return video, nil
}

// GetVideo retrieves video metadata by ID
func (r *VideoRepository) GetVideo(ctx context.Context, videoID string) (*VideoMetadata, error) {
	result, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("VIDEO#%s", videoID)},
			"sk": &types.AttributeValueMemberS{Value: "METADATA"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get video: %w", err)
	}

	if result.Item == nil {
		return nil, ErrVideoNotFound
	}

	var video VideoMetadata
	if err := attributevalue.UnmarshalMap(result.Item, &video); err != nil {
		return nil, fmt.Errorf("failed to unmarshal video: %w", err)
	}

	return &video, nil
}

// UpdateVideoProcessing marks a video as processing
func (r *VideoRepository) UpdateVideoProcessing(ctx context.Context, videoID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := r.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("VIDEO#%s", videoID)},
			"sk": &types.AttributeValueMemberS{Value: "METADATA"},
		},
		UpdateExpression: aws.String("SET #status = :status, updated_at = :updated_at"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status":     &types.AttributeValueMemberS{Value: string(StatusProcessing)},
			":updated_at": &types.AttributeValueMemberS{Value: now},
		},
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	if err != nil {
		var condErr *types.ConditionalCheckFailedException
		if errors.As(err, &condErr) {
			return ErrVideoNotFound
		}
		return fmt.Errorf("failed to update video: %w", err)
	}

	return nil
}

// CompleteVideoProcessing marks a video as completed and updates the latest pointer
func (r *VideoRepository) CompleteVideoProcessing(ctx context.Context, videoID, playbackURL, hlsPrefix string, presets []QualityPreset) error {
	now := time.Now().UTC().Format(time.RFC3339)

	presetsAV, err := attributevalue.MarshalList(presets)
	if err != nil {
		return fmt.Errorf("failed to marshal presets: %w", err)
	}

	// Update video record
	_, err = r.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("VIDEO#%s", videoID)},
			"sk": &types.AttributeValueMemberS{Value: "METADATA"},
		},
		UpdateExpression: aws.String(`
			SET #status = :status, 
			    updated_at = :updated_at, 
			    processed_at = :processed_at,
			    playback_url = :playback_url,
			    s3_hls_prefix = :hls_prefix,
			    quality_presets = :presets
		`),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status":       &types.AttributeValueMemberS{Value: string(StatusCompleted)},
			":updated_at":   &types.AttributeValueMemberS{Value: now},
			":processed_at": &types.AttributeValueMemberS{Value: now},
			":playback_url": &types.AttributeValueMemberS{Value: playbackURL},
			":hls_prefix":   &types.AttributeValueMemberS{Value: hlsPrefix},
			":presets":      &types.AttributeValueMemberL{Value: presetsAV},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to complete video: %w", err)
	}

	// Update LATEST pointer
	latestItem := map[string]types.AttributeValue{
		"pk":           &types.AttributeValueMemberS{Value: "LATEST"},
		"sk":           &types.AttributeValueMemberS{Value: "VIDEO"},
		"video_id":     &types.AttributeValueMemberS{Value: videoID},
		"playback_url": &types.AttributeValueMemberS{Value: playbackURL},
		"processed_at": &types.AttributeValueMemberS{Value: now},
	}

	_, err = r.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(r.tableName),
		Item:      latestItem,
	})
	if err != nil {
		return fmt.Errorf("failed to update latest pointer: %w", err)
	}

	return nil
}

// FailVideoProcessing marks a video as failed
func (r *VideoRepository) FailVideoProcessing(ctx context.Context, videoID, errorMessage string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := r.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("VIDEO#%s", videoID)},
			"sk": &types.AttributeValueMemberS{Value: "METADATA"},
		},
		UpdateExpression: aws.String("SET #status = :status, updated_at = :updated_at, error_message = :error"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status":     &types.AttributeValueMemberS{Value: string(StatusFailed)},
			":updated_at": &types.AttributeValueMemberS{Value: now},
			":error":      &types.AttributeValueMemberS{Value: errorMessage},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to mark video as failed: %w", err)
	}

	return nil
}

// GetLatestVideo retrieves the most recently processed video (O(1) operation)
func (r *VideoRepository) GetLatestVideo(ctx context.Context) (*VideoMetadata, error) {
	// First, get the LATEST pointer
	result, err := r.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(r.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: "LATEST"},
			"sk": &types.AttributeValueMemberS{Value: "VIDEO"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get latest video pointer: %w", err)
	}

	if result.Item == nil {
		return nil, ErrVideoNotFound
	}

	// Extract video ID from pointer
	videoIDAttr, ok := result.Item["video_id"]
	if !ok {
		return nil, ErrVideoNotFound
	}

	videoIDVal, ok := videoIDAttr.(*types.AttributeValueMemberS)
	if !ok {
		return nil, errors.New("invalid video_id type")
	}

	// Get full video metadata
	return r.GetVideo(ctx, videoIDVal.Value)
}

// ListVideos retrieves videos in reverse chronological order
func (r *VideoRepository) ListVideos(ctx context.Context, limit int32, startKey map[string]types.AttributeValue) ([]VideoMetadata, map[string]types.AttributeValue, error) {
	input := &dynamodb.QueryInput{
		TableName:              aws.String(r.tableName),
		IndexName:              aws.String("GSI1"),
		KeyConditionExpression: aws.String("gsi1pk = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: "ALL_VIDEOS"},
		},
		ScanIndexForward: aws.Bool(false), // Descending order (newest first)
		Limit:            aws.Int32(limit),
	}

	if startKey != nil {
		input.ExclusiveStartKey = startKey
	}

	result, err := r.client.Query(ctx, input)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list videos: %w", err)
	}

	var videos []VideoMetadata
	if err := attributevalue.UnmarshalListOfMaps(result.Items, &videos); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal videos: %w", err)
	}

	return videos, result.LastEvaluatedKey, nil
}
