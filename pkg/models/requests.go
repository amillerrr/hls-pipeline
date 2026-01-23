package models

import (
	"time"
)

// WebhookPayload represents a webhook notification payload.
type WebhookPayload struct {
	Event     string      `json:"event"`
	VideoID   string      `json:"videoId"`
	Status    string      `json:"status"`
	Timestamp time.Time   `json:"timestamp"`
	Video     *Video      `json:"video,omitempty"`
	Error     *ErrorInfo  `json:"error,omitempty"`
	Live      *LiveInfo   `json:"live,omitempty"`
}

// ErrorInfo contains error details for webhook notifications.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// LiveInfo contains live stream information for webhook notifications.
type LiveInfo struct {
	StreamID   string           `json:"streamId"`
	StreamName string           `json:"streamName"`
	State      string           `json:"state"`
	InputURL   string           `json:"inputUrl,omitempty"`
	OutputURL  string           `json:"outputUrl,omitempty"`
	Stats      *LiveStreamStats `json:"stats,omitempty"`
}

// LiveStreamStats contains statistics for a live stream.
type LiveStreamStats struct {
	BytesReceived   int64   `json:"bytesReceived"`
	BytesSent       int64   `json:"bytesSent"`
	FramesReceived  int64   `json:"framesReceived"`
	FramesDropped   int64   `json:"framesDropped"`
	Bitrate         int64   `json:"bitrate"`
	DurationSeconds float64 `json:"durationSeconds"`
	SegmentsCreated int64   `json:"segmentsCreated"`
	CurrentLatency  float64 `json:"currentLatency"`
}

// WebhookEvent constants for different event types.
const (
	// VOD Events
	WebhookEventVideoCreated    = "video.created"
	WebhookEventVideoProcessing = "video.processing"
	WebhookEventVideoCompleted  = "video.completed"
	WebhookEventVideoFailed     = "video.failed"

	// Live Events
	WebhookEventStreamCreated = "stream.created"
	WebhookEventStreamStarted = "stream.started"
	WebhookEventStreamStopped = "stream.stopped"
	WebhookEventStreamError   = "stream.error"

	// DRM Events
	WebhookEventDRMKeyRotated = "drm.key_rotated"

	// CDN Events
	WebhookEventCDNFailover = "cdn.failover"
)

// UploadRequest represents a client upload initiation request.
type UploadRequest struct {
	Filename    string            `json:"filename" validate:"required"`
	ContentType string            `json:"contentType" validate:"required"`
	Size        int64             `json:"size" validate:"required,min=1"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Options     *UploadOptions    `json:"options,omitempty"`
}

// UploadOptions contains optional processing options for uploads.
type UploadOptions struct {
	// Processing options
	EnableLLHLS bool     `json:"enableLlhls,omitempty"`
	EnableCMAF  bool     `json:"enableCmaf,omitempty"`
	Presets     []string `json:"presets,omitempty"`

	// DRM options
	EnableDRM  bool     `json:"enableDrm,omitempty"`
	DRMSystems []string `json:"drmSystems,omitempty"`

	// SSAI options
	EnableSSAI bool `json:"enableSsai,omitempty"`

	// Callback
	CallbackURL string `json:"callbackUrl,omitempty"`

	// Priority (higher = processed first)
	Priority int `json:"priority,omitempty"`
}

// UploadResponse represents the response to an upload initiation request.
type UploadResponse struct {
	VideoID       string            `json:"videoId"`
	UploadURL     string            `json:"uploadUrl"`
	UploadMethod  string            `json:"uploadMethod"`
	UploadHeaders map[string]string `json:"uploadHeaders,omitempty"`
	ExpiresAt     time.Time         `json:"expiresAt"`
}

// MultipartUploadResponse represents the response for multipart uploads.
type MultipartUploadResponse struct {
	VideoID    string                 `json:"videoId"`
	UploadID   string                 `json:"uploadId"`
	Key        string                 `json:"key"`
	Parts      []MultipartUploadPart  `json:"parts"`
	ExpiresAt  time.Time              `json:"expiresAt"`
}

// MultipartUploadPart represents a single part in a multipart upload.
type MultipartUploadPart struct {
	PartNumber int    `json:"partNumber"`
	UploadURL  string `json:"uploadUrl"`
}

// CompleteMultipartRequest is the request to complete a multipart upload.
type CompleteMultipartRequest struct {
	UploadID string              `json:"uploadId" validate:"required"`
	Parts    []CompletedPart     `json:"parts" validate:"required,min=1"`
}

// CompletedPart represents a completed part in a multipart upload.
type CompletedPart struct {
	PartNumber int    `json:"partNumber" validate:"required"`
	ETag       string `json:"etag" validate:"required"`
}

// VideoListResponse represents a paginated list of videos.
type VideoListResponse struct {
	Videos     []*Video `json:"videos"`
	Total      int      `json:"total"`
	Page       int      `json:"page"`
	PageSize   int      `json:"pageSize"`
	HasMore    bool     `json:"hasMore"`
	NextCursor string   `json:"nextCursor,omitempty"`
}

// VideoFilter contains filter options for listing videos.
type VideoFilter struct {
	Status    string    `json:"status,omitempty"`
	UserID    string    `json:"userId,omitempty"`
	CreatedAfter  *time.Time `json:"createdAfter,omitempty"`
	CreatedBefore *time.Time `json:"createdBefore,omitempty"`
}

// LiveStreamRequest represents a request to create a live stream.
type LiveStreamRequest struct {
	Name            string   `json:"name" validate:"required"`
	InputType       string   `json:"inputType,omitempty"` // srt, rtmp, rtsp
	Presets         []string `json:"presets,omitempty"`
	EnableLLHLS     bool     `json:"enableLlhls,omitempty"`
	EnableDRM       bool     `json:"enableDrm,omitempty"`
	DVRWindowSize   int      `json:"dvrWindowSize,omitempty"`
	EnableRecording bool     `json:"enableRecording,omitempty"`
	CallbackURL     string   `json:"callbackUrl,omitempty"`
}

// LiveStreamResponse represents a live stream response.
type LiveStreamResponse struct {
	StreamID   string            `json:"streamId"`
	Name       string            `json:"name"`
	State      string            `json:"state"`
	InputType  string            `json:"inputType"`
	InputURL   string            `json:"inputUrl"`
	OutputURL  string            `json:"outputUrl,omitempty"`
	RTMPUrl    string            `json:"rtmpUrl,omitempty"`
	SRTUrl     string            `json:"srtUrl,omitempty"`
	StartedAt  *time.Time        `json:"startedAt,omitempty"`
	Stats      *LiveStreamStats  `json:"stats,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}
