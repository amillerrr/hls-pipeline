package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Worker metrics
var (
	// VideosProcessed counts the total number of videos processed by status.
	VideosProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hls",
			Name:      "videos_processed_total",
			Help:      "Total number of videos processed",
		},
		[]string{"status"},
	)

	// ProcessingDuration tracks the time taken to process videos.
	ProcessingDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "hls",
			Name:      "video_processing_duration_seconds",
			Help:      "Time taken to process videos",
			Buckets:   []float64{10, 30, 60, 120, 300, 600},
		},
		[]string{"resolution"},
	)

	// QualityScore tracks the SSIM quality score for processed videos.
	QualityScore = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "hls",
			Name:      "video_quality_score",
			Help:      "Quality score (SSIM) for processed videos",
		},
		[]string{"metric_type"},
	)

	// ActiveJobs tracks the number of currently processing jobs.
	ActiveJobs = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "hls",
			Name:      "active_jobs",
			Help:      "Number of currently processing jobs",
		},
	)

	// DownloadDuration tracks the time taken to download videos from S3.
	DownloadDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "hls",
			Name:      "video_download_duration_seconds",
			Help:      "Time taken to download videos from S3",
			Buckets:   []float64{1, 5, 10, 30, 60, 120},
		},
	)

	// UploadDuration tracks the time taken to upload HLS files to S3.
	UploadDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "hls",
			Name:      "video_upload_duration_seconds",
			Help:      "Time taken to upload HLS files to S3",
			Buckets:   []float64{1, 5, 10, 30, 60, 120},
		},
	)

	// TranscodeDuration tracks the time taken for FFmpeg transcoding.
	TranscodeDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "hls",
			Name:      "video_transcode_duration_seconds",
			Help:      "Time taken for FFmpeg transcoding",
			Buckets:   []float64{10, 30, 60, 120, 300, 600, 1200},
		},
	)
)

// API metrics
var (
	// HTTPRequestsTotal counts HTTP requests by method, path, and status.
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hls",
			Subsystem: "api",
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestDuration tracks HTTP request duration.
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "hls",
			Subsystem: "api",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request duration in seconds",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	// AuthFailures counts authentication failures by type.
	AuthFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hls",
			Subsystem: "api",
			Name:      "auth_failures_total",
			Help:      "Total number of authentication failures",
		},
		[]string{"reason"},
	)

	// UploadsInitiated counts upload initiations.
	UploadsInitiated = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "hls",
			Subsystem: "api",
			Name:      "uploads_initiated_total",
			Help:      "Total number of uploads initiated",
		},
	)

	// UploadsCompleted counts completed uploads.
	UploadsCompleted = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "hls",
			Subsystem: "api",
			Name:      "uploads_completed_total",
			Help:      "Total number of uploads completed",
		},
	)
)

// RecordSuccess records a successful video processing.
func RecordSuccess() {
	VideosProcessed.WithLabelValues("success").Inc()
}

// RecordFailure records a failed video processing.
func RecordFailure() {
	VideosProcessed.WithLabelValues("failed").Inc()
}

// RecordQuality records the SSIM quality score.
func RecordQuality(metricType string, score float64) {
	QualityScore.WithLabelValues(metricType).Set(score)
}
