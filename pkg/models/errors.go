package models

import "errors"

// Sentinel errors for video operations.
var (
	// Validation errors
	ErrMissingVideoID = errors.New("videoId is required")
	ErrMissingS3Key   = errors.New("s3Key is required")
	ErrMissingBucket  = errors.New("bucket is required")

	// Processing errors
	ErrJobParseFailed  = errors.New("failed to parse job")
	ErrDownloadFailed  = errors.New("failed to download video")
	ErrTranscodeFailed = errors.New("failed to transcode video")
	ErrUploadFailed    = errors.New("failed to upload HLS files")
	ErrFFmpegFailed    = errors.New("ffmpeg execution failed")
	ErrContextCanceled = errors.New("context canceled")

	// Storage errors
	ErrVideoNotFound = errors.New("video not found")
	ErrInvalidStatus = errors.New("invalid video status")

	// Validation errors for uploads
	ErrInvalidFileType    = errors.New("invalid file type")
	ErrFilenameTooLong    = errors.New("filename too long")
	ErrInvalidContentType = errors.New("invalid content type")
	ErrInvalidKeyFormat   = errors.New("invalid key format")
)
