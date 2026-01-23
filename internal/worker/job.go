package worker

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JobSource indicates the origin of a job.
type JobSource string

const (
	// JobSourceS3Event indicates the job was triggered by an S3 event.
	JobSourceS3Event JobSource = "s3_event"
	// JobSourceAPI indicates the job was triggered by an API call.
	JobSourceAPI JobSource = "api"
)

// JobMessage represents a transcoding job from SQS.
type JobMessage struct {
	// Source indicates where the job originated
	Source JobSource `json:"source"`

	// Video identification
	VideoID string `json:"videoId"`

	// S3 location
	Bucket string `json:"bucket"`
	Key    string `json:"key"`
	Region string `json:"region,omitempty"`

	// File metadata
	Size        int64  `json:"size,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	ETag        string `json:"etag,omitempty"`

	// Event timestamp
	EventTime time.Time `json:"eventTime,omitempty"`

	// Processing options
	Options *JobOptions `json:"options,omitempty"`

	// Custom metadata from upload
	Metadata map[string]string `json:"metadata,omitempty"`
}

// JobOptions contains optional processing parameters.
type JobOptions struct {
	// Output configuration
	OutputBucket string `json:"outputBucket,omitempty"`
	OutputPrefix string `json:"outputPrefix,omitempty"`

	// Transcoding options
	Presets         []string `json:"presets,omitempty"`
	EnableLLHLS     bool     `json:"enableLlhls,omitempty"`
	EnableCMAF      bool     `json:"enableCmaf,omitempty"`
	SegmentDuration float64  `json:"segmentDuration,omitempty"`
	PartDuration    float64  `json:"partDuration,omitempty"`

	// DRM options
	EnableDRM        bool     `json:"enableDrm,omitempty"`
	DRMSystems       []string `json:"drmSystems,omitempty"`
	EncryptionScheme string   `json:"encryptionScheme,omitempty"`

	// SSAI options
	EnableSSAI     bool `json:"enableSsai,omitempty"`
	PreserveSCTE35 bool `json:"preserveScte35,omitempty"`

	// Webhook callback
	CallbackURL   string `json:"callbackUrl,omitempty"`
	WebhookSecret string `json:"-"` // Not serialized

	// Priority (higher = more urgent)
	Priority int `json:"priority,omitempty"`
}

// ParseSQSMessage parses an SQS message body and returns a JobMessage.
// It handles both S3 event notifications and direct API messages.
func ParseSQSMessage(body string) (*JobMessage, error) {
	// First, try to parse as S3 event notification
	if strings.Contains(body, "s3:ObjectCreated") || strings.Contains(body, "\"Records\"") {
		return parseS3Event(body)
	}

	// Otherwise, parse as direct API message
	return parseAPIMessage(body)
}

// parseS3Event parses an S3 event notification message.
func parseS3Event(body string) (*JobMessage, error) {
	var notification S3EventNotification
	if err := json.Unmarshal([]byte(body), &notification); err != nil {
		return nil, fmt.Errorf("failed to parse S3 event: %w", err)
	}

	if len(notification.Records) == 0 {
		return nil, fmt.Errorf("no records in S3 event")
	}

	record := notification.Records[0]

	// Extract video ID from the key (e.g., uploads/abc123/video.mp4 -> abc123)
	videoID := extractVideoID(record.S3.Object.Key)

	job := &JobMessage{
		Source:      JobSourceS3Event,
		VideoID:     videoID,
		Bucket:      record.S3.Bucket.Name,
		Key:         record.S3.Object.Key,
		Region:      record.AWSRegion,
		Size:        record.S3.Object.Size,
		ContentType: record.S3.Object.ContentType,
		ETag:        record.S3.Object.ETag,
		EventTime:   record.EventTime,
	}

	// Extract metadata from S3 user metadata if present
	if record.S3.Object.UserMetadata != nil {
		job.Metadata = record.S3.Object.UserMetadata
	}

	return job, nil
}

// parseAPIMessage parses a direct API job message.
func parseAPIMessage(body string) (*JobMessage, error) {
	var job JobMessage
	if err := json.Unmarshal([]byte(body), &job); err != nil {
		return nil, fmt.Errorf("failed to parse API message: %w", err)
	}

	job.Source = JobSourceAPI

	return &job, nil
}

// extractVideoID extracts the video ID from an S3 key.
// Expected format: uploads/{videoId}/filename.ext or processed/{videoId}/...
func extractVideoID(key string) string {
	parts := strings.Split(key, "/")

	// Look for the ID after known prefixes
	for i, part := range parts {
		if (part == "uploads" || part == "raw") && i+1 < len(parts) {
			return parts[i+1]
		}
	}

	// If no known prefix, try to extract from the path
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}

	// Fallback: generate from filename
	if len(parts) >= 1 {
		filename := parts[len(parts)-1]
		if idx := strings.LastIndex(filename, "."); idx > 0 {
			return filename[:idx]
		}
		return filename
	}

	return ""
}

// S3EventNotification represents an S3 event notification.
type S3EventNotification struct {
	Records []S3EventRecord `json:"Records"`
}

// S3EventRecord represents a single S3 event record.
type S3EventRecord struct {
	EventVersion string    `json:"eventVersion"`
	EventSource  string    `json:"eventSource"`
	AWSRegion    string    `json:"awsRegion"`
	EventTime    time.Time `json:"eventTime"`
	EventName    string    `json:"eventName"`
	S3           S3Entity  `json:"s3"`
}

// S3Entity represents the S3 portion of an event record.
type S3Entity struct {
	Bucket S3BucketInfo `json:"bucket"`
	Object S3ObjectInfo `json:"object"`
}

// S3BucketInfo contains S3 bucket information.
type S3BucketInfo struct {
	Name string `json:"name"`
	ARN  string `json:"arn"`
}

// S3ObjectInfo contains S3 object information.
type S3ObjectInfo struct {
	Key          string            `json:"key"`
	Size         int64             `json:"size"`
	ETag         string            `json:"eTag"`
	ContentType  string            `json:"contentType,omitempty"`
	UserMetadata map[string]string `json:"userMetadata,omitempty"`
}

// JobStatus represents the status of a job.
type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
	JobStatusCancelled  JobStatus = "cancelled"
)

// JobProgress represents the progress of a job.
type JobProgress struct {
	JobID       string     `json:"jobId"`
	VideoID     string     `json:"videoId"`
	Status      JobStatus  `json:"status"`
	Progress    float64    `json:"progress"` // 0-100
	Stage       string     `json:"stage"`
	Message     string     `json:"message,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// WebhookPayload represents the payload sent to webhook callbacks.
type WebhookPayload struct {
	Event     string      `json:"event"` // job.completed, job.failed, job.progress
	Timestamp time.Time   `json:"timestamp"`
	VideoID   string      `json:"videoId"`
	JobID     string      `json:"jobId,omitempty"`
	Status    JobStatus   `json:"status"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// Validate validates the job message.
func (j *JobMessage) Validate() error {
	if j.Bucket == "" {
		return fmt.Errorf("bucket is required")
	}
	if j.Key == "" {
		return fmt.Errorf("key is required")
	}
	return nil
}

// GetVideoID returns the video ID, generating one if necessary.
func (j *JobMessage) GetVideoID() string {
	if j.VideoID != "" {
		return j.VideoID
	}
	return extractVideoID(j.Key)
}

// IsVideoFile checks if the S3 object is a video file based on extension.
func (j *JobMessage) IsVideoFile() bool {
	key := strings.ToLower(j.Key)
	videoExtensions := []string{".mp4", ".mov", ".mkv", ".avi", ".webm", ".m4v", ".ts", ".mts"}

	for _, ext := range videoExtensions {
		if strings.HasSuffix(key, ext) {
			return true
		}
	}

	return false
}

// GetOutputPrefix returns the output prefix for processed files.
func (j *JobMessage) GetOutputPrefix() string {
	if j.Options != nil && j.Options.OutputPrefix != "" {
		return j.Options.OutputPrefix
	}
	return fmt.Sprintf("processed/%s", j.GetVideoID())
}

// GetOutputBucket returns the output bucket for processed files.
func (j *JobMessage) GetOutputBucket(defaultBucket string) string {
	if j.Options != nil && j.Options.OutputBucket != "" {
		return j.Options.OutputBucket
	}
	return defaultBucket
}
