package models

import (
	"time"
)

// VideoStatus represents the processing status of a video.
type VideoStatus string

const (
	StatusPending    VideoStatus = "PENDING"
	StatusProcessing VideoStatus = "PROCESSING"
	StatusTranscoding VideoStatus = "TRANSCODING"
	StatusPackaging  VideoStatus = "PACKAGING"
	StatusEncrypting VideoStatus = "ENCRYPTING"
	StatusCompleted  VideoStatus = "COMPLETED"
	StatusFailed     VideoStatus = "FAILED"
	StatusCancelled  VideoStatus = "CANCELLED"
)

// Video represents a video in the system.
type Video struct {
	ID              string            `json:"id" dynamodbav:"id"`
	UserID          string            `json:"userId,omitempty" dynamodbav:"userId,omitempty"`
	Title           string            `json:"title,omitempty" dynamodbav:"title,omitempty"`
	Description     string            `json:"description,omitempty" dynamodbav:"description,omitempty"`
	Status          VideoStatus       `json:"status" dynamodbav:"status"`
	SourceKey       string            `json:"sourceKey,omitempty" dynamodbav:"sourceKey,omitempty"`
	OutputPrefix    string            `json:"outputPrefix,omitempty" dynamodbav:"outputPrefix,omitempty"`
	HLSManifestURL  string            `json:"hlsManifestUrl,omitempty" dynamodbav:"hlsManifestUrl,omitempty"`
	DASHManifestURL string            `json:"dashManifestUrl,omitempty" dynamodbav:"dashManifestUrl,omitempty"`
	ThumbnailURL    string            `json:"thumbnailUrl,omitempty" dynamodbav:"thumbnailUrl,omitempty"`
	Duration        float64           `json:"duration,omitempty" dynamodbav:"duration,omitempty"`
	FileSize        int64             `json:"fileSize,omitempty" dynamodbav:"fileSize,omitempty"`
	OutputSize      int64             `json:"outputSize,omitempty" dynamodbav:"outputSize,omitempty"`
	Width           int               `json:"width,omitempty" dynamodbav:"width,omitempty"`
	Height          int               `json:"height,omitempty" dynamodbav:"height,omitempty"`
	FrameRate       float64           `json:"frameRate,omitempty" dynamodbav:"frameRate,omitempty"`
	Bitrate         int64             `json:"bitrate,omitempty" dynamodbav:"bitrate,omitempty"`
	VideoCodec      string            `json:"videoCodec,omitempty" dynamodbav:"videoCodec,omitempty"`
	AudioCodec      string            `json:"audioCodec,omitempty" dynamodbav:"audioCodec,omitempty"`
	CreatedAt       time.Time         `json:"createdAt" dynamodbav:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt" dynamodbav:"updatedAt"`
	CompletedAt     *time.Time        `json:"completedAt,omitempty" dynamodbav:"completedAt,omitempty"`
	ErrorMessage    string            `json:"errorMessage,omitempty" dynamodbav:"errorMessage,omitempty"`
	ErrorCode       string            `json:"errorCode,omitempty" dynamodbav:"errorCode,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty" dynamodbav:"metadata,omitempty"`
	Tags            []string          `json:"tags,omitempty" dynamodbav:"tags,omitempty"`

	// Processing options
	Options *VideoOptions `json:"options,omitempty" dynamodbav:"options,omitempty"`

	// Output information
	Outputs *VideoOutputs `json:"outputs,omitempty" dynamodbav:"outputs,omitempty"`

	// DRM information
	DRM *VideoDRM `json:"drm,omitempty" dynamodbav:"drm,omitempty"`

	// SSAI information
	SSAI *VideoSSAI `json:"ssai,omitempty" dynamodbav:"ssai,omitempty"`
}

// VideoOptions contains processing options for a video.
type VideoOptions struct {
	// Transcoding options
	EnableLLHLS       bool     `json:"enableLlhls,omitempty" dynamodbav:"enableLlhls,omitempty"`
	EnableCMAF        bool     `json:"enableCmaf,omitempty" dynamodbav:"enableCmaf,omitempty"`
	SegmentDuration   float64  `json:"segmentDuration,omitempty" dynamodbav:"segmentDuration,omitempty"`
	PartDuration      float64  `json:"partDuration,omitempty" dynamodbav:"partDuration,omitempty"`
	Presets           []string `json:"presets,omitempty" dynamodbav:"presets,omitempty"`
	EnableThumbnails  bool     `json:"enableThumbnails,omitempty" dynamodbav:"enableThumbnails,omitempty"`
	ThumbnailInterval float64  `json:"thumbnailInterval,omitempty" dynamodbav:"thumbnailInterval,omitempty"`

	// DRM options
	EnableDRM         bool     `json:"enableDrm,omitempty" dynamodbav:"enableDrm,omitempty"`
	DRMSystems        []string `json:"drmSystems,omitempty" dynamodbav:"drmSystems,omitempty"` // widevine, fairplay, playready
	EncryptionScheme  string   `json:"encryptionScheme,omitempty" dynamodbav:"encryptionScheme,omitempty"` // cenc, cbcs

	// SSAI options
	EnableSSAI        bool   `json:"enableSsai,omitempty" dynamodbav:"enableSsai,omitempty"`
	PreserveSCTE35    bool   `json:"preserveScte35,omitempty" dynamodbav:"preserveScte35,omitempty"`
	ContentID         string `json:"contentId,omitempty" dynamodbav:"contentId,omitempty"`

	// CDN options
	PreferredCDN      string `json:"preferredCdn,omitempty" dynamodbav:"preferredCdn,omitempty"`

	// Callback options
	CallbackURL       string `json:"callbackUrl,omitempty" dynamodbav:"callbackUrl,omitempty"`
	WebhookSecret     string `json:"-" dynamodbav:"webhookSecret,omitempty"` // Not serialized to JSON
}

// VideoOutputs contains output information for a video.
type VideoOutputs struct {
	Variants       []VariantOutput `json:"variants,omitempty" dynamodbav:"variants,omitempty"`
	IFramePlaylists []string       `json:"iframePlaylists,omitempty" dynamodbav:"iframePlaylists,omitempty"`
	AudioRenditions []AudioOutput  `json:"audioRenditions,omitempty" dynamodbav:"audioRenditions,omitempty"`
	Thumbnails      []string       `json:"thumbnails,omitempty" dynamodbav:"thumbnails,omitempty"`
}

// VariantOutput represents a single variant/rendition output.
type VariantOutput struct {
	Name         string  `json:"name" dynamodbav:"name"`
	PlaylistURL  string  `json:"playlistUrl" dynamodbav:"playlistUrl"`
	Width        int     `json:"width" dynamodbav:"width"`
	Height       int     `json:"height" dynamodbav:"height"`
	Bandwidth    int64   `json:"bandwidth" dynamodbav:"bandwidth"`
	AvgBandwidth int64   `json:"avgBandwidth,omitempty" dynamodbav:"avgBandwidth,omitempty"`
	FrameRate    float64 `json:"frameRate,omitempty" dynamodbav:"frameRate,omitempty"`
	Codecs       string  `json:"codecs" dynamodbav:"codecs"`
	SegmentCount int     `json:"segmentCount,omitempty" dynamodbav:"segmentCount,omitempty"`
}

// AudioOutput represents an audio rendition output.
type AudioOutput struct {
	Name       string `json:"name" dynamodbav:"name"`
	Language   string `json:"language" dynamodbav:"language"`
	Channels   int    `json:"channels" dynamodbav:"channels"`
	Bandwidth  int64  `json:"bandwidth" dynamodbav:"bandwidth"`
	Codecs     string `json:"codecs" dynamodbav:"codecs"`
	PlaylistURL string `json:"playlistUrl" dynamodbav:"playlistUrl"`
}

// VideoDRM contains DRM information for a video.
type VideoDRM struct {
	Enabled          bool      `json:"enabled" dynamodbav:"enabled"`
	KeyID            string    `json:"keyId,omitempty" dynamodbav:"keyId,omitempty"`
	Systems          []string  `json:"systems,omitempty" dynamodbav:"systems,omitempty"`
	EncryptionScheme string    `json:"encryptionScheme,omitempty" dynamodbav:"encryptionScheme,omitempty"`
	KeyRotation      bool      `json:"keyRotation,omitempty" dynamodbav:"keyRotation,omitempty"`
	LicenseURLs      *DRMLicenseURLs `json:"licenseUrls,omitempty" dynamodbav:"licenseUrls,omitempty"`
}

// DRMLicenseURLs contains license URLs for each DRM system.
type DRMLicenseURLs struct {
	Widevine  string `json:"widevine,omitempty" dynamodbav:"widevine,omitempty"`
	FairPlay  string `json:"fairplay,omitempty" dynamodbav:"fairplay,omitempty"`
	PlayReady string `json:"playready,omitempty" dynamodbav:"playready,omitempty"`
}

// VideoSSAI contains SSAI information for a video.
type VideoSSAI struct {
	Enabled           bool        `json:"enabled" dynamodbav:"enabled"`
	ConfigName        string      `json:"configName,omitempty" dynamodbav:"configName,omitempty"`
	SessionURL        string      `json:"sessionUrl,omitempty" dynamodbav:"sessionUrl,omitempty"`
	AdBreaks          []AdBreakInfo `json:"adBreaks,omitempty" dynamodbav:"adBreaks,omitempty"`
	SCTE35Markers     int         `json:"scte35Markers,omitempty" dynamodbav:"scte35Markers,omitempty"`
}

// AdBreakInfo contains information about an ad break.
type AdBreakInfo struct {
	StartTime     float64 `json:"startTime" dynamodbav:"startTime"`
	Duration      float64 `json:"duration" dynamodbav:"duration"`
	SpliceEventID uint32  `json:"spliceEventId,omitempty" dynamodbav:"spliceEventId,omitempty"`
}

// UploadRequest represents a request to upload a video.
type UploadRequest struct {
	Filename    string            `json:"filename" validate:"required"`
	ContentType string            `json:"contentType" validate:"required"`
	FileSize    int64             `json:"fileSize" validate:"required,min=1"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Options     *VideoOptions     `json:"options,omitempty"`
}

// UploadResponse contains the response for an upload request.
type UploadResponse struct {
	VideoID     string            `json:"videoId"`
	UploadURL   string            `json:"uploadUrl"`
	UploadFields map[string]string `json:"uploadFields,omitempty"` // For multipart uploads
	ExpiresAt   time.Time         `json:"expiresAt"`
}

// TranscodeJob represents a transcoding job message.
type TranscodeJob struct {
	VideoID     string        `json:"videoId"`
	SourceKey   string        `json:"sourceKey"`
	Options     *VideoOptions `json:"options,omitempty"`
	Priority    int           `json:"priority,omitempty"`
	RetryCount  int           `json:"retryCount,omitempty"`
	CreatedAt   time.Time     `json:"createdAt"`
}

// VideoListResponse contains a paginated list of videos.
type VideoListResponse struct {
	Videos     []Video `json:"videos"`
	NextToken  string  `json:"nextToken,omitempty"`
	TotalCount int     `json:"totalCount,omitempty"`
}

// VideoStats contains statistics about videos.
type VideoStats struct {
	TotalVideos      int   `json:"totalVideos"`
	ProcessingVideos int   `json:"processingVideos"`
	CompletedVideos  int   `json:"completedVideos"`
	FailedVideos     int   `json:"failedVideos"`
	TotalDuration    int64 `json:"totalDuration"` // seconds
	TotalStorage     int64 `json:"totalStorage"`  // bytes
}

// WebhookPayload represents the payload sent to callback URLs.
type WebhookPayload struct {
	Event     string    `json:"event"`
	VideoID   string    `json:"videoId"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Video     *Video    `json:"video,omitempty"`
	Error     *WebhookError `json:"error,omitempty"`
}

// WebhookError contains error information for webhook payloads.
type WebhookError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// IsTerminal returns true if the status is terminal (completed or failed).
func (s VideoStatus) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// IsProcessing returns true if the video is currently being processed.
func (s VideoStatus) IsProcessing() bool {
	return s == StatusProcessing || s == StatusTranscoding || 
		s == StatusPackaging || s == StatusEncrypting
}

// NewVideo creates a new Video with defaults.
func NewVideo(id string) *Video {
	now := time.Now()
	return &Video{
		ID:        id,
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  make(map[string]string),
	}
}

// SetStatus updates the video status and UpdatedAt timestamp.
func (v *Video) SetStatus(status VideoStatus) {
	v.Status = status
	v.UpdatedAt = time.Now()
	
	if status == StatusCompleted {
		now := time.Now()
		v.CompletedAt = &now
	}
}

// SetError sets the error information on the video.
func (v *Video) SetError(code, message string) {
	v.Status = StatusFailed
	v.ErrorCode = code
	v.ErrorMessage = message
	v.UpdatedAt = time.Now()
}

// HasDRM returns true if the video has DRM enabled.
func (v *Video) HasDRM() bool {
	return v.DRM != nil && v.DRM.Enabled
}

// HasSSAI returns true if the video has SSAI enabled.
func (v *Video) HasSSAI() bool {
	return v.SSAI != nil && v.SSAI.Enabled
}

