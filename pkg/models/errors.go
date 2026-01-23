package models

import "errors"

// Validation Errors
var (
	// ErrInvalidFileType is returned when the uploaded file type is not supported.
	ErrInvalidFileType = errors.New("invalid file type")

	// ErrInvalidContentType is returned when the content type is not supported.
	ErrInvalidContentType = errors.New("invalid content type")

	// ErrFilenameTooLong is returned when the filename exceeds the maximum length.
	ErrFilenameTooLong = errors.New("filename exceeds maximum length of 255 characters")

	// ErrInvalidKeyFormat is returned when the S3 key format is invalid.
	ErrInvalidKeyFormat = errors.New("invalid S3 key format")

	// ErrInvalidVideoID is returned when the video ID is invalid.
	ErrInvalidVideoID = errors.New("invalid video ID format")

	// ErrMissingRequiredField is returned when a required field is missing.
	ErrMissingRequiredField = errors.New("missing required field")

	// ErrInvalidDuration is returned when video duration is invalid or exceeds limits.
	ErrInvalidDuration = errors.New("invalid video duration")

	// ErrInvalidResolution is returned when video resolution is invalid.
	ErrInvalidResolution = errors.New("invalid video resolution")

	// ErrFileTooLarge is returned when file size exceeds the maximum allowed.
	ErrFileTooLarge = errors.New("file size exceeds maximum allowed")
)

// Storage Errors
var (
	// ErrVideoNotFound is returned when a video is not found in storage.
	ErrVideoNotFound = errors.New("video not found")

	// ErrVideoAlreadyExists is returned when trying to create a video that already exists.
	ErrVideoAlreadyExists = errors.New("video already exists")

	// ErrStorageUnavailable is returned when storage is unavailable.
	ErrStorageUnavailable = errors.New("storage unavailable")

	// ErrStorageTimeout is returned when a storage operation times out.
	ErrStorageTimeout = errors.New("storage operation timed out")

	// ErrObjectNotFound is returned when an S3 object is not found.
	ErrObjectNotFound = errors.New("object not found in S3")

	// ErrBucketNotFound is returned when an S3 bucket is not found.
	ErrBucketNotFound = errors.New("S3 bucket not found")

	// ErrPermissionDenied is returned when access is denied.
	ErrPermissionDenied = errors.New("permission denied")
)

// Transcoding Errors
var (
	// ErrTranscodingFailed is returned when video transcoding fails.
	ErrTranscodingFailed = errors.New("video transcoding failed")

	// ErrUnsupportedCodec is returned when the video codec is not supported.
	ErrUnsupportedCodec = errors.New("unsupported video codec")

	// ErrUnsupportedFormat is returned when the video format is not supported.
	ErrUnsupportedFormat = errors.New("unsupported video format")

	// ErrFFmpegNotFound is returned when FFmpeg is not installed.
	ErrFFmpegNotFound = errors.New("FFmpeg not found")

	// ErrPackagerNotFound is returned when Shaka Packager is not installed.
	ErrPackagerNotFound = errors.New("Shaka Packager not found")

	// ErrTranscodingTimeout is returned when transcoding times out.
	ErrTranscodingTimeout = errors.New("transcoding operation timed out")

	// ErrInvalidPreset is returned when a transcoding preset is invalid.
	ErrInvalidPreset = errors.New("invalid transcoding preset")

	// ErrOutputPathInvalid is returned when the output path is invalid.
	ErrOutputPathInvalid = errors.New("invalid output path")
)

// CDN Errors
var (
	// ErrCDNUnavailable is returned when no CDN is available.
	ErrCDNUnavailable = errors.New("no CDN available")

	// ErrCDNHealthCheckFailed is returned when a CDN health check fails.
	ErrCDNHealthCheckFailed = errors.New("CDN health check failed")

	// ErrInvalidCDNConfig is returned when CDN configuration is invalid.
	ErrInvalidCDNConfig = errors.New("invalid CDN configuration")

	// ErrCDNTimeout is returned when a CDN request times out.
	ErrCDNTimeout = errors.New("CDN request timed out")

	// ErrInvalidManifest is returned when manifest manipulation fails.
	ErrInvalidManifest = errors.New("invalid manifest format")
)

// DRM Errors
var (
	// ErrDRMProviderUnavailable is returned when the DRM provider is unavailable.
	ErrDRMProviderUnavailable = errors.New("DRM provider unavailable")

	// ErrInvalidCPIXDocument is returned when a CPIX document is invalid.
	ErrInvalidCPIXDocument = errors.New("invalid CPIX document")

	// ErrKeyNotFound is returned when a DRM key is not found.
	ErrKeyNotFound = errors.New("DRM key not found")

	// ErrLicenseRequestFailed is returned when a license request fails.
	ErrLicenseRequestFailed = errors.New("license request failed")

	// ErrUnsupportedDRMSystem is returned when the DRM system is not supported.
	ErrUnsupportedDRMSystem = errors.New("unsupported DRM system")

	// ErrInvalidKeyFormat is returned when the key format is invalid.
	ErrInvalidDRMKeyFormat = errors.New("invalid DRM key format")

	// ErrCertificateNotFound is returned when a DRM certificate is not found.
	ErrCertificateNotFound = errors.New("DRM certificate not found")
)

// SSAI Errors
var (
	// ErrAdServerUnavailable is returned when the ad server is unavailable.
	ErrAdServerUnavailable = errors.New("ad server unavailable")

	// ErrInvalidVASTResponse is returned when a VAST response is invalid.
	ErrInvalidVASTResponse = errors.New("invalid VAST response")

	// ErrAdInsertionFailed is returned when ad insertion fails.
	ErrAdInsertionFailed = errors.New("ad insertion failed")

	// ErrInvalidSCTE35Marker is returned when a SCTE-35 marker is invalid.
	ErrInvalidSCTE35Marker = errors.New("invalid SCTE-35 marker")

	// ErrMediaTailorError is returned for MediaTailor-specific errors.
	ErrMediaTailorError = errors.New("MediaTailor error")

	// ErrSessionNotFound is returned when a MediaTailor session is not found.
	ErrSessionNotFound = errors.New("session not found")
)

// Queue Errors
var (
	// ErrQueueUnavailable is returned when the message queue is unavailable.
	ErrQueueUnavailable = errors.New("message queue unavailable")

	// ErrMessageInvalid is returned when a queue message is invalid.
	ErrMessageInvalid = errors.New("invalid queue message")

	// ErrMessageProcessingFailed is returned when message processing fails.
	ErrMessageProcessingFailed = errors.New("message processing failed")

	// ErrMaxRetriesExceeded is returned when max retries are exceeded.
	ErrMaxRetriesExceeded = errors.New("maximum retries exceeded")
)

// Authentication Errors
var (
	// ErrInvalidToken is returned when a JWT token is invalid.
	ErrInvalidToken = errors.New("invalid token")

	// ErrTokenExpired is returned when a JWT token has expired.
	ErrTokenExpired = errors.New("token expired")

	// ErrUnauthorized is returned when authentication fails.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrInvalidCredentials is returned when credentials are invalid.
	ErrInvalidCredentials = errors.New("invalid credentials")
)

// Error Wrapping Helpers
// WrapError wraps an error with additional context.
func WrapError(err error, message string) error {
	if err == nil {
		return nil
	}
	return &wrappedError{
		err:     err,
		message: message,
	}
}

type wrappedError struct {
	err     error
	message string
}

func (e *wrappedError) Error() string {
	return e.message + ": " + e.err.Error()
}

func (e *wrappedError) Unwrap() error {
	return e.err
}

// IsRetryable returns true if the error is retryable.
func IsRetryable(err error) bool {
	switch {
	case errors.Is(err, ErrStorageTimeout),
		errors.Is(err, ErrStorageUnavailable),
		errors.Is(err, ErrCDNTimeout),
		errors.Is(err, ErrCDNUnavailable),
		errors.Is(err, ErrQueueUnavailable),
		errors.Is(err, ErrDRMProviderUnavailable),
		errors.Is(err, ErrAdServerUnavailable):
		return true
	default:
		return false
	}
}

// IsPermanent returns true if the error is permanent and should not be retried.
func IsPermanent(err error) bool {
	switch {
	case errors.Is(err, ErrInvalidFileType),
		errors.Is(err, ErrInvalidContentType),
		errors.Is(err, ErrUnsupportedCodec),
		errors.Is(err, ErrUnsupportedFormat),
		errors.Is(err, ErrInvalidPreset),
		errors.Is(err, ErrInvalidCPIXDocument),
		errors.Is(err, ErrUnsupportedDRMSystem):
		return true
	default:
		return false
	}
}
