package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// S3Event represents an S3 event notification message.
type S3Event struct {
	Records []S3EventRecord `json:"Records"`
}

// S3EventRecord represents a single S3 event record.
type S3EventRecord struct {
	EventVersion string    `json:"eventVersion"`
	EventSource  string    `json:"eventSource"`
	AWSRegion    string    `json:"awsRegion"`
	EventTime    time.Time `json:"eventTime"`
	EventName    string    `json:"eventName"`
	UserIdentity struct {
		PrincipalID string `json:"principalId"`
	} `json:"userIdentity"`
	RequestParameters struct {
		SourceIPAddress string `json:"sourceIPAddress"`
	} `json:"requestParameters"`
	ResponseElements struct {
		RequestID string `json:"x-amz-request-id"`
		HostID    string `json:"x-amz-id-2"`
	} `json:"responseElements"`
	S3 S3EventS3 `json:"s3"`
}

// S3EventS3 contains S3-specific event information.
type S3EventS3 struct {
	SchemaVersion   string        `json:"s3SchemaVersion"`
	ConfigurationID string        `json:"configurationId"`
	Bucket          S3EventBucket `json:"bucket"`
	Object          S3EventObject `json:"object"`
}

// S3EventBucket contains bucket information.
type S3EventBucket struct {
	Name          string `json:"name"`
	OwnerIdentity struct {
		PrincipalID string `json:"principalId"`
	} `json:"ownerIdentity"`
	ARN string `json:"arn"`
}

// S3EventObject contains object information.
type S3EventObject struct {
	Key           string `json:"key"`
	Size          int64  `json:"size"`
	ETag          string `json:"eTag"`
	ContentType   string `json:"contentType,omitempty"`
	VersionID     string `json:"versionId,omitempty"`
	Sequencer     string `json:"sequencer"`
	URLDecodedKey string `json:"-"` // Populated after parsing
}

// SQSMessage wraps an SQS message that may contain S3 events.
type SQSMessage struct {
	Type             string `json:"Type"`
	MessageID        string `json:"MessageId"`
	TopicARN         string `json:"TopicArn,omitempty"`
	Subject          string `json:"Subject,omitempty"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp,omitempty"`
	SignatureVersion string `json:"SignatureVersion,omitempty"`
	Signature        string `json:"Signature,omitempty"`
	SigningCertURL   string `json:"SigningCertURL,omitempty"`
	UnsubscribeURL   string `json:"UnsubscribeURL,omitempty"`
}

// JobMessage represents a unified job message (either S3 event or direct API trigger).
type JobMessage struct {
	// Source indicates where this job came from
	Source JobSource `json:"source"`

	// VideoID is set for API-triggered jobs
	VideoID string `json:"videoId,omitempty"`

	// S3 information (always present)
	Bucket    string    `json:"bucket"`
	Key       string    `json:"key"`
	Size      int64     `json:"size"`
	ETag      string    `json:"etag,omitempty"`
	Region    string    `json:"region,omitempty"`
	EventTime time.Time `json:"eventTime,omitempty"`

	// Processing options
	Options *JobOptions `json:"options,omitempty"`

	// Metadata extracted from S3 object tags or user metadata
	Metadata map[string]string `json:"metadata,omitempty"`
}

// JobSource indicates where the job originated.
type JobSource string

const (
	JobSourceS3Event JobSource = "s3_event"
	JobSourceAPI     JobSource = "api"
	JobSourceLive    JobSource = "live"
)

// JobOptions contains processing options for a job.
type JobOptions struct {
	EnableLLHLS  bool     `json:"enableLlhls,omitempty"`
	EnableCMAF   bool     `json:"enableCmaf,omitempty"`
	EnableDRM    bool     `json:"enableDrm,omitempty"`
	DRMSystems   []string `json:"drmSystems,omitempty"`
	EnableSSAI   bool     `json:"enableSsai,omitempty"`
	Presets      []string `json:"presets,omitempty"`
	CallbackURL  string   `json:"callbackUrl,omitempty"`
	Priority     int      `json:"priority,omitempty"`
	OutputPrefix string   `json:"outputPrefix,omitempty"`
}

// ParseSQSMessage parses an SQS message body and returns a JobMessage.
// It handles both direct S3 events and SNS-wrapped S3 events.
func ParseSQSMessage(body string) (*JobMessage, error) {
	// First, try to parse as a direct job message (API-triggered)
	var directJob JobMessage
	if err := json.Unmarshal([]byte(body), &directJob); err == nil {
		if directJob.VideoID != "" && directJob.Key != "" {
			directJob.Source = JobSourceAPI
			return &directJob, nil
		}
	}

	// Try to parse as SNS notification wrapping S3 event
	var snsMsg SQSMessage
	if err := json.Unmarshal([]byte(body), &snsMsg); err == nil && snsMsg.Type == "Notification" {
		// Extract the actual S3 event from the SNS message
		body = snsMsg.Message
	}

	// Parse as S3 event
	var s3Event S3Event
	if err := json.Unmarshal([]byte(body), &s3Event); err != nil {
		return nil, fmt.Errorf("failed to parse message: %w", err)
	}

	if len(s3Event.Records) == 0 {
		return nil, fmt.Errorf("no S3 event records found")
	}

	// Process the first record (typically there's only one per message)
	record := s3Event.Records[0]

	// URL-decode the key (S3 URL-encodes special characters)
	decodedKey, err := url.QueryUnescape(record.S3.Object.Key)
	if err != nil {
		decodedKey = record.S3.Object.Key
	}

	// Validate this is a supported video file
	if !isVideoFile(decodedKey) {
		return nil, fmt.Errorf("unsupported file type: %s", filepath.Ext(decodedKey))
	}

	// Extract video ID from the key path
	// Expected format: uploads/{videoId}/{filename} or {videoId}/{filename}
	videoID := extractVideoIDFromKey(decodedKey)

	return &JobMessage{
		Source:    JobSourceS3Event,
		VideoID:   videoID,
		Bucket:    record.S3.Bucket.Name,
		Key:       decodedKey,
		Size:      record.S3.Object.Size,
		ETag:      strings.Trim(record.S3.Object.ETag, "\""),
		Region:    record.AWSRegion,
		EventTime: record.EventTime,
	}, nil
}

// isVideoFile checks if the file extension indicates a video file.
func isVideoFile(key string) bool {
	ext := strings.ToLower(filepath.Ext(key))
	supportedExtensions := map[string]bool{
		".mp4":  true,
		".mov":  true,
		".avi":  true,
		".mkv":  true,
		".webm": true,
		".m4v":  true,
		".ts":   true,
		".mts":  true,
		".m2ts": true,
		".wmv":  true,
		".flv":  true,
		".mpg":  true,
		".mpeg": true,
		".3gp":  true,
		".mxf":  true,
	}
	return supportedExtensions[ext]
}

// extractVideoIDFromKey extracts the video ID from an S3 key.
func extractVideoIDFromKey(key string) string {
	// Remove common prefixes
	key = strings.TrimPrefix(key, "uploads/")
	key = strings.TrimPrefix(key, "raw/")
	key = strings.TrimPrefix(key, "input/")

	// Split by path separator
	parts := strings.Split(key, "/")

	// If there's a directory structure, use the first part as video ID
	if len(parts) > 1 {
		return parts[0]
	}

	// Otherwise, use the filename without extension as video ID
	return strings.TrimSuffix(parts[0], filepath.Ext(parts[0]))
}

// S3EventFilter provides filtering for S3 events.
type S3EventFilter struct {
	// AllowedPrefixes limits processing to keys with these prefixes
	AllowedPrefixes []string

	// DeniedPrefixes excludes keys with these prefixes
	DeniedPrefixes []string

	// AllowedExtensions limits processing to these file extensions
	AllowedExtensions []string

	// MinSize is the minimum file size in bytes
	MinSize int64

	// MaxSize is the maximum file size in bytes (0 = unlimited)
	MaxSize int64
}

// DefaultS3EventFilter returns the default filter for video uploads.
func DefaultS3EventFilter() *S3EventFilter {
	return &S3EventFilter{
		AllowedPrefixes: []string{"uploads/", "raw/", "input/"},
		DeniedPrefixes:  []string{"processed/", "output/", "thumbnails/", "temp/", "."},
		AllowedExtensions: []string{
			".mp4", ".mov", ".avi", ".mkv", ".webm", ".m4v",
			".ts", ".mts", ".m2ts", ".wmv", ".flv", ".mpg",
			".mpeg", ".3gp", ".mxf",
		},
		MinSize: 1024,                    // 1KB minimum
		MaxSize: 50 * 1024 * 1024 * 1024, // 50GB maximum
	}
}

// ShouldProcess checks if an S3 event should be processed.
func (f *S3EventFilter) ShouldProcess(job *JobMessage) bool {
	key := job.Key

	// Check denied prefixes first
	for _, prefix := range f.DeniedPrefixes {
		if strings.HasPrefix(key, prefix) {
			return false
		}
	}

	// Check allowed prefixes (if specified)
	if len(f.AllowedPrefixes) > 0 {
		allowed := false
		for _, prefix := range f.AllowedPrefixes {
			if strings.HasPrefix(key, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}

	// Check file extension
	if len(f.AllowedExtensions) > 0 {
		ext := strings.ToLower(filepath.Ext(key))
		allowed := false
		for _, allowedExt := range f.AllowedExtensions {
			if ext == allowedExt {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}

	// Check file size
	if f.MinSize > 0 && job.Size < f.MinSize {
		return false
	}
	if f.MaxSize > 0 && job.Size > f.MaxSize {
		return false
	}

	return true
}

// S3EventProcessor processes S3 events from SQS.
type S3EventProcessor struct {
	filter *S3EventFilter
	logger *slog.Logger
}

// NewS3EventProcessor creates a new S3 event processor.
func NewS3EventProcessor(filter *S3EventFilter, logger *slog.Logger) *S3EventProcessor {
	if filter == nil {
		filter = DefaultS3EventFilter()
	}
	return &S3EventProcessor{
		filter: filter,
		logger: logger,
	}
}

// ProcessMessage processes an SQS message and returns a job if applicable.
func (p *S3EventProcessor) ProcessMessage(ctx context.Context, body string) (*JobMessage, error) {
	job, err := ParseSQSMessage(body)
	if err != nil {
		p.logger.Debug("failed to parse message", "error", err)
		return nil, err
	}

	// Apply filter
	if !p.filter.ShouldProcess(job) {
		p.logger.Debug("message filtered out",
			"key", job.Key,
			"size", job.Size,
		)
		return nil, nil // Return nil job to indicate filtered out (not an error)
	}

	p.logger.Info("processing S3 event",
		"source", job.Source,
		"bucket", job.Bucket,
		"key", job.Key,
		"size", job.Size,
		"videoId", job.VideoID,
	)

	return job, nil
}

// IsS3Event checks if the message body looks like an S3 event.
func IsS3Event(body string) bool {
	return strings.Contains(body, `"eventSource":"aws:s3"`) ||
		strings.Contains(body, `"s3SchemaVersion"`)
}

// IsTestEvent checks if this is an S3 test event (sent when configuring notifications).
func IsTestEvent(body string) bool {
	return strings.Contains(body, `"Event":"s3:TestEvent"`)
}
