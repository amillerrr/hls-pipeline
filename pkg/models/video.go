package models

import (
	"time"
)

// Video represents a video in the system.
type Video struct {
	// ID is the unique identifier for the video.
	ID string `json:"id" dynamodbav:"id"`

	// Title is the video title.
	Title string `json:"title" dynamodbav:"title"`

	// Description is the video description.
	Description string `json:"description,omitempty" dynamodbav:"description,omitempty"`

	// Status is the current processing status.
	Status string `json:"status" dynamodbav:"status"`

	// Progress is the processing progress (0-100).
	Progress int `json:"progress,omitempty" dynamodbav:"progress,omitempty"`

	// S3Key is the key of the original video in S3.
	S3Key string `json:"s3Key" dynamodbav:"s3Key"`

	// OutputPath is the path to the processed HLS output.
	OutputPath string `json:"outputPath,omitempty" dynamodbav:"outputPath,omitempty"`

	// MasterPlaylistURL is the URL to the HLS master playlist.
	MasterPlaylistURL string `json:"masterPlaylistUrl,omitempty" dynamodbav:"masterPlaylistUrl,omitempty"`

	// Duration is the video duration in seconds.
	Duration float64 `json:"duration,omitempty" dynamodbav:"duration,omitempty"`

	// Width is the video width in pixels.
	Width int `json:"width,omitempty" dynamodbav:"width,omitempty"`

	// Height is the video height in pixels.
	Height int `json:"height,omitempty" dynamodbav:"height,omitempty"`

	// Bitrate is the original video bitrate in bps.
	Bitrate int64 `json:"bitrate,omitempty" dynamodbav:"bitrate,omitempty"`

	// Codec is the original video codec.
	Codec string `json:"codec,omitempty" dynamodbav:"codec,omitempty"`

	// FileSize is the original file size in bytes.
	FileSize int64 `json:"fileSize,omitempty" dynamodbav:"fileSize,omitempty"`

	// OutputSize is the total output size in bytes.
	OutputSize int64 `json:"outputSize,omitempty" dynamodbav:"outputSize,omitempty"`

	// ThumbnailURL is the URL to the video thumbnail.
	ThumbnailURL string `json:"thumbnailUrl,omitempty" dynamodbav:"thumbnailUrl,omitempty"`

	// Variants contains information about each HLS variant.
	Variants []VideoVariant `json:"variants,omitempty" dynamodbav:"variants,omitempty"`

	// DRMEnabled indicates if DRM is enabled for this video.
	DRMEnabled bool `json:"drmEnabled,omitempty" dynamodbav:"drmEnabled,omitempty"`

	// DRMKeyID is the DRM key ID if DRM is enabled.
	DRMKeyID string `json:"drmKeyId,omitempty" dynamodbav:"drmKeyId,omitempty"`

	// SSAIEnabled indicates if SSAI is enabled for this video.
	SSAIEnabled bool `json:"ssaiEnabled,omitempty" dynamodbav:"ssaiEnabled,omitempty"`

	// AdBreaks contains the ad break definitions.
	AdBreaks []AdBreakConfig `json:"adBreaks,omitempty" dynamodbav:"adBreaks,omitempty"`

	// Metadata contains custom metadata.
	Metadata map[string]string `json:"metadata,omitempty" dynamodbav:"metadata,omitempty"`

	// Tags are searchable tags for the video.
	Tags []string `json:"tags,omitempty" dynamodbav:"tags,omitempty"`

	// ErrorMessage contains the error message if processing failed.
	ErrorMessage string `json:"errorMessage,omitempty" dynamodbav:"errorMessage,omitempty"`

	// CreatedAt is when the video was created.
	CreatedAt time.Time `json:"createdAt" dynamodbav:"createdAt"`

	// UpdatedAt is when the video was last updated.
	UpdatedAt time.Time `json:"updatedAt" dynamodbav:"updatedAt"`

	// ProcessedAt is when processing completed.
	ProcessedAt *time.Time `json:"processedAt,omitempty" dynamodbav:"processedAt,omitempty"`

	// UserID is the ID of the user who uploaded the video.
	UserID string `json:"userId,omitempty" dynamodbav:"userId,omitempty"`
}

// VideoVariant represents an HLS variant stream.
type VideoVariant struct {
	// Name is the variant name (e.g., "1080p", "720p").
	Name string `json:"name" dynamodbav:"name"`

	// Resolution is the resolution string (e.g., "1920x1080").
	Resolution string `json:"resolution" dynamodbav:"resolution"`

	// Width is the variant width in pixels.
	Width int `json:"width" dynamodbav:"width"`

	// Height is the variant height in pixels.
	Height int `json:"height" dynamodbav:"height"`

	// Bitrate is the video bitrate in bps.
	Bitrate int64 `json:"bitrate" dynamodbav:"bitrate"`

	// AudioBitrate is the audio bitrate in bps.
	AudioBitrate int64 `json:"audioBitrate" dynamodbav:"audioBitrate"`

	// Codec is the video codec string.
	Codec string `json:"codec" dynamodbav:"codec"`

	// PlaylistPath is the relative path to the variant playlist.
	PlaylistPath string `json:"playlistPath" dynamodbav:"playlistPath"`

	// SegmentCount is the number of segments in this variant.
	SegmentCount int `json:"segmentCount,omitempty" dynamodbav:"segmentCount,omitempty"`
}

// AdBreakConfig represents an ad break configuration.
type AdBreakConfig struct {
	// ID is the ad break identifier.
	ID string `json:"id" dynamodbav:"id"`

	// Type is the ad break type (preroll, midroll, postroll).
	Type string `json:"type" dynamodbav:"type"`

	// StartTime is the start time in seconds.
	StartTime float64 `json:"startTime" dynamodbav:"startTime"`

	// Duration is the maximum duration in seconds.
	Duration float64 `json:"duration" dynamodbav:"duration"`

	// SCTE35 is the optional SCTE-35 payload.
	SCTE35 string `json:"scte35,omitempty" dynamodbav:"scte35,omitempty"`
}

// VideoStatus constants.
const (
	VideoStatusPending    = "pending"
	VideoStatusProcessing = "processing"
	VideoStatusCompleted  = "completed"
	VideoStatusFailed     = "failed"
	VideoStatusDeleted    = "deleted"
)

// ProcessingJob represents a video processing job.
type ProcessingJob struct {
	// VideoID is the ID of the video being processed.
	VideoID string `json:"videoId"`

	// S3Key is the S3 key of the source video.
	S3Key string `json:"s3Key"`

	// Bucket is the S3 bucket containing the source video.
	Bucket string `json:"bucket"`

	// Title is the video title.
	Title string `json:"title,omitempty"`

	// Metadata contains custom metadata.
	Metadata map[string]string `json:"metadata,omitempty"`

	// Timestamp is when the job was created.
	Timestamp int64 `json:"timestamp"`

	// Options contains processing options.
	Options ProcessingOptions `json:"options,omitempty"`
}

// ProcessingOptions contains options for video processing.
type ProcessingOptions struct {
	// EnableLLHLS enables Low-Latency HLS output.
	EnableLLHLS bool `json:"enableLlhls,omitempty"`

	// EnableCMAF enables CMAF (fMP4) segments.
	EnableCMAF bool `json:"enableCmaf,omitempty"`

	// EnableDRM enables DRM encryption.
	EnableDRM bool `json:"enableDrm,omitempty"`

	// EnableSSAI enables server-side ad insertion markers.
	EnableSSAI bool `json:"enableSsai,omitempty"`

	// SegmentDuration is the target segment duration.
	SegmentDuration float64 `json:"segmentDuration,omitempty"`

	// PartDuration is the LL-HLS part duration.
	PartDuration float64 `json:"partDuration,omitempty"`

	// Presets are the encoding presets to use.
	Presets []string `json:"presets,omitempty"`

	// AdBreakTimes are the times for ad break markers.
	AdBreakTimes []float64 `json:"adBreakTimes,omitempty"`
}

// PlaybackRequest represents a playback request.
type PlaybackRequest struct {
	// VideoID is the ID of the video to play.
	VideoID string `json:"videoId"`

	// SessionID is an optional session identifier.
	SessionID string `json:"sessionId,omitempty"`

	// DRMSystem is the requested DRM system.
	DRMSystem string `json:"drmSystem,omitempty"`

	// AdParams are parameters for ad targeting.
	AdParams map[string]string `json:"adParams,omitempty"`

	// DeviceType is the device type for analytics.
	DeviceType string `json:"deviceType,omitempty"`
}

// PlaybackResponse contains playback URLs and metadata.
type PlaybackResponse struct {
	// VideoID is the ID of the video.
	VideoID string `json:"videoId"`

	// HLSManifestURL is the HLS master playlist URL.
	HLSManifestURL string `json:"hlsManifestUrl"`

	// DASHManifestURL is the DASH manifest URL.
	DASHManifestURL string `json:"dashManifestUrl,omitempty"`

	// DRMLicenseURL is the DRM license server URL.
	DRMLicenseURL string `json:"drmLicenseUrl,omitempty"`

	// SessionID is the playback session ID.
	SessionID string `json:"sessionId,omitempty"`

	// Duration is the video duration.
	Duration float64 `json:"duration"`

	// Variants contains available quality variants.
	Variants []VideoVariant `json:"variants,omitempty"`
}
