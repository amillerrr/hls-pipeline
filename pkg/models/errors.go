package models

import (
	"errors"
	"fmt"
	"net/http"
)

// Common error codes
const (
	ErrCodeInvalidInput      = "INVALID_INPUT"
	ErrCodeNotFound          = "NOT_FOUND"
	ErrCodeAlreadyExists     = "ALREADY_EXISTS"
	ErrCodeUnauthorized      = "UNAUTHORIZED"
	ErrCodeForbidden         = "FORBIDDEN"
	ErrCodeRateLimited       = "RATE_LIMITED"
	ErrCodeInternal          = "INTERNAL_ERROR"
	ErrCodeUploadFailed      = "UPLOAD_FAILED"
	ErrCodeTranscodeFailed   = "TRANSCODE_FAILED"
	ErrCodePackagingFailed   = "PACKAGING_FAILED"
	ErrCodeEncryptionFailed  = "ENCRYPTION_FAILED"
	ErrCodeDRMFailed         = "DRM_FAILED"
	ErrCodeCDNFailed         = "CDN_FAILED"
	ErrCodeSSAIFailed        = "SSAI_FAILED"
	ErrCodeStorageFailed     = "STORAGE_FAILED"
	ErrCodeQueueFailed       = "QUEUE_FAILED"
	ErrCodeTimeout           = "TIMEOUT"
	ErrCodeCancelled         = "CANCELLED"
	ErrCodeFFmpegFailed      = "FFMPEG_FAILED"
	ErrCodeShakaFailed       = "SHAKA_FAILED"
	ErrCodeInvalidFormat     = "INVALID_FORMAT"
	ErrCodeUnsupportedCodec  = "UNSUPPORTED_CODEC"
)

// Sentinel errors for common conditions
var (
	ErrVideoNotFound     = NewAppError(ErrCodeNotFound, "video not found", http.StatusNotFound)
	ErrInvalidVideoID    = NewAppError(ErrCodeInvalidInput, "invalid video ID", http.StatusBadRequest)
	ErrUnauthorized      = NewAppError(ErrCodeUnauthorized, "unauthorized", http.StatusUnauthorized)
	ErrForbidden         = NewAppError(ErrCodeForbidden, "forbidden", http.StatusForbidden)
	ErrRateLimited       = NewAppError(ErrCodeRateLimited, "rate limit exceeded", http.StatusTooManyRequests)
	ErrInternalServer    = NewAppError(ErrCodeInternal, "internal server error", http.StatusInternalServerError)
	ErrInvalidInput      = NewAppError(ErrCodeInvalidInput, "invalid input", http.StatusBadRequest)
)

// AppError represents an application-level error with context.
type AppError struct {
	Code       string            `json:"code"`
	Message    string            `json:"message"`
	Details    string            `json:"details,omitempty"`
	HTTPStatus int               `json:"-"`
	Err        error             `json:"-"`
	Context    map[string]string `json:"context,omitempty"`
}

// Error implements the error interface.
func (e *AppError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Details)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the wrapped error.
func (e *AppError) Unwrap() error {
	return e.Err
}

// Is allows error comparison with errors.Is.
func (e *AppError) Is(target error) bool {
	if t, ok := target.(*AppError); ok {
		return e.Code == t.Code
	}
	return false
}

// NewAppError creates a new application error.
func NewAppError(code, message string, httpStatus int) *AppError {
	return &AppError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
	}
}

// WithDetails adds details to the error.
func (e *AppError) WithDetails(details string) *AppError {
	return &AppError{
		Code:       e.Code,
		Message:    e.Message,
		Details:    details,
		HTTPStatus: e.HTTPStatus,
		Err:        e.Err,
		Context:    e.Context,
	}
}

// WithError wraps another error.
func (e *AppError) WithError(err error) *AppError {
	return &AppError{
		Code:       e.Code,
		Message:    e.Message,
		Details:    err.Error(),
		HTTPStatus: e.HTTPStatus,
		Err:        err,
		Context:    e.Context,
	}
}

// WithContext adds context to the error.
func (e *AppError) WithContext(key, value string) *AppError {
	ctx := make(map[string]string)
	for k, v := range e.Context {
		ctx[k] = v
	}
	ctx[key] = value
	
	return &AppError{
		Code:       e.Code,
		Message:    e.Message,
		Details:    e.Details,
		HTTPStatus: e.HTTPStatus,
		Err:        e.Err,
		Context:    ctx,
	}
}

// TranscodeError represents an error during transcoding.
type TranscodeError struct {
	VideoID string `json:"videoId"`
	Stage   string `json:"stage"`
	Err     error  `json:"-"`
	Details string `json:"details,omitempty"`
}

// Error implements the error interface.
func (e *TranscodeError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("transcode error for video %s at %s: %s", e.VideoID, e.Stage, e.Details)
	}
	if e.Err != nil {
		return fmt.Sprintf("transcode error for video %s at %s: %v", e.VideoID, e.Stage, e.Err)
	}
	return fmt.Sprintf("transcode error for video %s at %s", e.VideoID, e.Stage)
}

// Unwrap returns the wrapped error.
func (e *TranscodeError) Unwrap() error {
	return e.Err
}

// WrapTranscodeError creates a new TranscodeError wrapping an existing error.
func WrapTranscodeError(err error, videoID, stage string) error {
	return &TranscodeError{
		VideoID: videoID,
		Stage:   stage,
		Err:     err,
	}
}

// NewTranscodeError creates a new TranscodeError with details.
func NewTranscodeError(videoID, stage, details string) error {
	return &TranscodeError{
		VideoID: videoID,
		Stage:   stage,
		Details: details,
	}
}

// PackagingError represents an error during packaging.
type PackagingError struct {
	VideoID string `json:"videoId"`
	Format  string `json:"format"` // HLS, DASH, CMAF
	Err     error  `json:"-"`
}

// Error implements the error interface.
func (e *PackagingError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("packaging error for video %s (%s): %v", e.VideoID, e.Format, e.Err)
	}
	return fmt.Sprintf("packaging error for video %s (%s)", e.VideoID, e.Format)
}

// Unwrap returns the wrapped error.
func (e *PackagingError) Unwrap() error {
	return e.Err
}

// DRMError represents an error during DRM processing.
type DRMError struct {
	VideoID  string `json:"videoId"`
	System   string `json:"system"` // widevine, fairplay, playready
	KeyID    string `json:"keyId,omitempty"`
	Err      error  `json:"-"`
}

// Error implements the error interface.
func (e *DRMError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("DRM error for video %s (%s): %v", e.VideoID, e.System, e.Err)
	}
	return fmt.Sprintf("DRM error for video %s (%s)", e.VideoID, e.System)
}

// Unwrap returns the wrapped error.
func (e *DRMError) Unwrap() error {
	return e.Err
}

// CDNError represents an error during CDN operations.
type CDNError struct {
	Provider string `json:"provider"`
	URL      string `json:"url,omitempty"`
	Err      error  `json:"-"`
}

// Error implements the error interface.
func (e *CDNError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("CDN error for provider %s: %v", e.Provider, e.Err)
	}
	return fmt.Sprintf("CDN error for provider %s", e.Provider)
}

// Unwrap returns the wrapped error.
func (e *CDNError) Unwrap() error {
	return e.Err
}

// ValidationError represents a validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Value   any    `json:"value,omitempty"`
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error on field '%s': %s", e.Field, e.Message)
}

// ValidationErrors is a collection of validation errors.
type ValidationErrors []ValidationError

// Error implements the error interface.
func (e ValidationErrors) Error() string {
	if len(e) == 1 {
		return e[0].Error()
	}
	return fmt.Sprintf("%d validation errors occurred", len(e))
}

// Add adds a validation error.
func (e *ValidationErrors) Add(field, message string) {
	*e = append(*e, ValidationError{Field: field, Message: message})
}

// HasErrors returns true if there are any validation errors.
func (e ValidationErrors) HasErrors() bool {
	return len(e) > 0
}

// ToAppError converts validation errors to an AppError.
func (e ValidationErrors) ToAppError() *AppError {
	if len(e) == 0 {
		return nil
	}
	
	return NewAppError(ErrCodeInvalidInput, "validation failed", http.StatusBadRequest).
		WithDetails(e.Error())
}

// Error response helpers

// ErrorResponse represents an API error response.
type ErrorResponse struct {
	Error *ErrorDetail `json:"error"`
}

// ErrorDetail contains error details for API responses.
type ErrorDetail struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details string            `json:"details,omitempty"`
	Context map[string]string `json:"context,omitempty"`
}

// NewErrorResponse creates an error response from an error.
func NewErrorResponse(err error) *ErrorResponse {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return &ErrorResponse{
			Error: &ErrorDetail{
				Code:    appErr.Code,
				Message: appErr.Message,
				Details: appErr.Details,
				Context: appErr.Context,
			},
		}
	}

	return &ErrorResponse{
		Error: &ErrorDetail{
			Code:    ErrCodeInternal,
			Message: "internal server error",
		},
	}
}

// GetHTTPStatus returns the HTTP status code for an error.
func GetHTTPStatus(err error) int {
	var appErr *AppError
	if errors.As(err, &appErr) {
		if appErr.HTTPStatus > 0 {
			return appErr.HTTPStatus
		}
	}

	// Map error types to status codes
	var transcodeErr *TranscodeError
	var packagingErr *PackagingError
	var drmErr *DRMError
	var cdnErr *CDNError
	var validationErr *ValidationError
	var validationErrs ValidationErrors

	switch {
	case errors.As(err, &transcodeErr):
		return http.StatusInternalServerError
	case errors.As(err, &packagingErr):
		return http.StatusInternalServerError
	case errors.As(err, &drmErr):
		return http.StatusInternalServerError
	case errors.As(err, &cdnErr):
		return http.StatusBadGateway
	case errors.As(err, &validationErr):
		return http.StatusBadRequest
	case errors.As(err, &validationErrs):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// IsRetryable returns true if the error can be retried.
func IsRetryable(err error) bool {
	var appErr *AppError
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case ErrCodeTimeout, ErrCodeRateLimited, ErrCodeCDNFailed, ErrCodeQueueFailed:
			return true
		}
	}

	var cdnErr *CDNError
	if errors.As(err, &cdnErr) {
		return true
	}

	return false
}

// ShouldAlert returns true if the error should trigger an alert.
func ShouldAlert(err error) bool {
	var appErr *AppError
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case ErrCodeInternal, ErrCodeDRMFailed, ErrCodeFFmpegFailed, ErrCodeShakaFailed:
			return true
		}
	}

	var drmErr *DRMError
	if errors.As(err, &drmErr) {
		return true
	}

	return false
}

