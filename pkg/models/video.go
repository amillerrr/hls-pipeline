package models

// VideoStatus represents the processing status of a video.
type VideoStatus string

const (
	StatusPending    VideoStatus = "pending"
	StatusProcessing VideoStatus = "processing"
	StatusCompleted  VideoStatus = "completed"
	StatusFailed     VideoStatus = "failed"
)

// IsValid returns true if the status is a valid VideoStatus.
func (s VideoStatus) IsValid() bool {
	switch s {
	case StatusPending, StatusProcessing, StatusCompleted, StatusFailed:
		return true
	}
	return false
}

// VideoMetadata represents the full metadata for a video.
type VideoMetadata struct {
	// Keys
	PK     string `dynamodbav:"pk"`
	SK     string `dynamodbav:"sk"`
	GSI1PK string `dynamodbav:"gsi1pk,omitempty"`
	GSI1SK string `dynamodbav:"gsi1sk,omitempty"`

	// Attributes
	VideoID         string          `dynamodbav:"video_id" json:"videoId"`
	Filename        string          `dynamodbav:"filename" json:"filename"`
	Status          VideoStatus     `dynamodbav:"status" json:"status"`
	S3RawKey        string          `dynamodbav:"s3_raw_key" json:"s3RawKey"`
	S3HLSPrefix     string          `dynamodbav:"s3_hls_prefix,omitempty" json:"s3HlsPrefix,omitempty"`
	PlaybackURL     string          `dynamodbav:"playback_url,omitempty" json:"playbackUrl,omitempty"`
	FileSizeBytes   int64           `dynamodbav:"file_size_bytes,omitempty" json:"fileSizeBytes,omitempty"`
	DurationSeconds float64         `dynamodbav:"duration_seconds,omitempty" json:"durationSeconds,omitempty"`
	CreatedAt       string          `dynamodbav:"created_at" json:"createdAt"`
	UpdatedAt       string          `dynamodbav:"updated_at" json:"updatedAt"`
	ProcessedAt     string          `dynamodbav:"processed_at,omitempty" json:"processedAt,omitempty"`
	QualityPresets  []QualityPreset `dynamodbav:"quality_presets,omitempty" json:"qualityPresets,omitempty"`
	ErrorMessage    string          `dynamodbav:"error_message,omitempty" json:"errorMessage,omitempty"`
}

// QualityPreset represents a video quality level configuration.
type QualityPreset struct {
	Name    string `dynamodbav:"name" json:"name"`
	Width   int    `dynamodbav:"width" json:"width"`
	Height  int    `dynamodbav:"height" json:"height"`
	Bitrate int    `dynamodbav:"bitrate" json:"bitrate"`
}

// VideoJob represents a video processing job from SQS.
type VideoJob struct {
	VideoID  string `json:"videoId"`
	S3Key    string `json:"s3Key"`
	Bucket   string `json:"bucket"`
	Filename string `json:"filename"`
}

// Validate checks if the video job has all required fields.
func (j *VideoJob) Validate() error {
	if j.VideoID == "" {
		return ErrMissingVideoID
	}
	if j.S3Key == "" {
		return ErrMissingS3Key
	}
	if j.Bucket == "" {
		return ErrMissingBucket
	}
	return nil
}
