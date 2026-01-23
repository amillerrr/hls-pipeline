package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Namespace for all metrics
const namespace = "hls_pipeline"

// Transcoding metrics
var (
	// TranscodeDuration tracks transcoding job duration in seconds.
	TranscodeDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "transcode_duration_seconds",
		Help:      "Duration of transcoding jobs in seconds",
		Buckets:   []float64{10, 30, 60, 120, 300, 600, 1200, 1800, 3600},
	})

	// TranscodeJobsTotal tracks total transcoding jobs by status.
	TranscodeJobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "transcode_jobs_total",
		Help:      "Total number of transcoding jobs",
	}, []string{"status", "preset"})

	// TranscodeJobsInProgress tracks currently running transcoding jobs.
	TranscodeJobsInProgress = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "transcode_jobs_in_progress",
		Help:      "Number of transcoding jobs currently in progress",
	})

	// TranscodeInputBytes tracks input video bytes processed.
	TranscodeInputBytes = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "transcode_input_bytes_total",
		Help:      "Total input bytes processed",
	})

	// TranscodeOutputBytes tracks output bytes generated.
	TranscodeOutputBytes = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "transcode_output_bytes_total",
		Help:      "Total output bytes generated",
	})

	// TranscodeErrors tracks transcoding errors by type.
	TranscodeErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "transcode_errors_total",
		Help:      "Total number of transcoding errors",
	}, []string{"error_type", "stage"})
)

// Packaging metrics
var (
	// PackagingDuration tracks packaging duration in seconds.
	PackagingDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "packaging_duration_seconds",
		Help:      "Duration of packaging jobs in seconds",
		Buckets:   []float64{1, 5, 10, 30, 60, 120, 300},
	}, []string{"format"}) // hls, dash, cmaf

	// PackagingJobsTotal tracks total packaging jobs.
	PackagingJobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "packaging_jobs_total",
		Help:      "Total number of packaging jobs",
	}, []string{"status", "format"})

	// SegmentsGenerated tracks total segments generated.
	SegmentsGenerated = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "segments_generated_total",
		Help:      "Total number of segments generated",
	}, []string{"format", "type"}) // type: video, audio, init
)

// DRM metrics
var (
	// DRMKeyFetchDuration tracks DRM key fetch duration.
	DRMKeyFetchDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "drm_key_fetch_duration_seconds",
		Help:      "Duration of DRM key fetch operations",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2, 5, 10},
	}, []string{"provider"}) // local, buydrm, pallycon, axinom

	// DRMKeyFetchTotal tracks total DRM key fetch operations.
	DRMKeyFetchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "drm_key_fetch_total",
		Help:      "Total DRM key fetch operations",
	}, []string{"provider", "status"})

	// DRMEncryptionDuration tracks encryption duration.
	DRMEncryptionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "drm_encryption_duration_seconds",
		Help:      "Duration of DRM encryption operations",
		Buckets:   []float64{1, 5, 10, 30, 60, 120},
	}, []string{"system", "scheme"}) // system: widevine/fairplay/playready, scheme: cenc/cbcs

	// DRMEncryptedVideos tracks encrypted videos.
	DRMEncryptedVideos = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "drm_encrypted_videos_total",
		Help:      "Total number of videos encrypted",
	}, []string{"system"})
)

// CDN metrics
var (
	// CDNHealthStatus tracks CDN health status (1 = healthy, 0 = unhealthy).
	CDNHealthStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "cdn_health_status",
		Help:      "CDN provider health status (1 = healthy, 0 = unhealthy)",
	}, []string{"provider"})

	// CDNLatency tracks CDN latency.
	CDNLatency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "cdn_latency_seconds",
		Help:      "CDN provider latency in seconds",
	}, []string{"provider"})

	// CDNRequestsTotal tracks total CDN requests.
	CDNRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cdn_requests_total",
		Help:      "Total CDN requests",
	}, []string{"provider", "status"})

	// CDNFailovers tracks CDN failover events.
	CDNFailovers = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cdn_failovers_total",
		Help:      "Total CDN failover events",
	}, []string{"from_provider", "to_provider"})

	// CDNSessionsActive tracks active CDN sessions.
	CDNSessionsActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "cdn_sessions_active",
		Help:      "Number of active CDN sessions",
	}, []string{"provider"})
)

// SSAI metrics
var (
	// SSAISessionsTotal tracks total SSAI sessions.
	SSAISessionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "ssai_sessions_total",
		Help:      "Total SSAI sessions created",
	}, []string{"status"})

	// SSAIAdBreaks tracks ad breaks served.
	SSAIAdBreaks = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "ssai_ad_breaks_total",
		Help:      "Total ad breaks served",
	})

	// SSAIAdDuration tracks total ad duration served.
	SSAIAdDuration = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "ssai_ad_duration_seconds_total",
		Help:      "Total ad duration served in seconds",
	})

	// SCTE35MarkersProcessed tracks SCTE-35 markers processed.
	SCTE35MarkersProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "scte35_markers_processed_total",
		Help:      "Total SCTE-35 markers processed",
	}, []string{"type"}) // cue_out, cue_in, time_signal
)

// API metrics
var (
	// HTTPRequestsTotal tracks total HTTP requests.
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "http_requests_total",
		Help:      "Total HTTP requests",
	}, []string{"method", "endpoint", "status"})

	// HTTPRequestDuration tracks HTTP request duration.
	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request duration in seconds",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"method", "endpoint"})

	// HTTPRequestSize tracks HTTP request sizes.
	HTTPRequestSize = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_request_size_bytes",
		Help:      "HTTP request size in bytes",
		Buckets:   []float64{100, 1000, 10000, 100000, 1000000, 10000000},
	}, []string{"method", "endpoint"})

	// HTTPResponseSize tracks HTTP response sizes.
	HTTPResponseSize = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "http_response_size_bytes",
		Help:      "HTTP response size in bytes",
		Buckets:   []float64{100, 1000, 10000, 100000, 1000000, 10000000},
	}, []string{"method", "endpoint"})
)

// Storage metrics
var (
	// S3OperationsTotal tracks S3 operations.
	S3OperationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "s3_operations_total",
		Help:      "Total S3 operations",
	}, []string{"operation", "bucket", "status"})

	// S3OperationDuration tracks S3 operation duration.
	S3OperationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "s3_operation_duration_seconds",
		Help:      "S3 operation duration in seconds",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"operation", "bucket"})

	// S3BytesTransferred tracks bytes transferred to/from S3.
	S3BytesTransferred = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "s3_bytes_transferred_total",
		Help:      "Total bytes transferred to/from S3",
	}, []string{"direction", "bucket"}) // direction: upload, download
)

// Queue metrics
var (
	// SQSMessagesTotal tracks SQS messages.
	SQSMessagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "sqs_messages_total",
		Help:      "Total SQS messages",
	}, []string{"queue", "operation", "status"})

	// SQSMessageAge tracks the age of messages when processed.
	SQSMessageAge = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "sqs_message_age_seconds",
		Help:      "Age of SQS messages when processed",
		Buckets:   []float64{1, 5, 10, 30, 60, 120, 300, 600, 1800},
	})

	// SQSQueueDepth tracks the approximate queue depth.
	SQSQueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "sqs_queue_depth",
		Help:      "Approximate number of messages in queue",
	}, []string{"queue"})
)

// Worker metrics
var (
	// WorkerPoolSize tracks the worker pool size.
	WorkerPoolSize = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "worker_pool_size",
		Help:      "Current worker pool size",
	})

	// WorkerBusy tracks number of busy workers.
	WorkerBusy = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "worker_busy",
		Help:      "Number of busy workers",
	})

	// WorkerJobDuration tracks worker job duration.
	WorkerJobDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "worker_job_duration_seconds",
		Help:      "Duration of worker jobs in seconds",
		Buckets:   []float64{10, 30, 60, 120, 300, 600, 1200, 1800, 3600},
	})
)

// RecordTranscodeJob records a transcode job completion.
func RecordTranscodeJob(status, preset string, duration float64, inputBytes, outputBytes int64) {
	TranscodeJobsTotal.WithLabelValues(status, preset).Inc()
	TranscodeDuration.Observe(duration)
	TranscodeInputBytes.Add(float64(inputBytes))
	TranscodeOutputBytes.Add(float64(outputBytes))
}

// RecordTranscodeError records a transcode error.
func RecordTranscodeError(errorType, stage string) {
	TranscodeErrors.WithLabelValues(errorType, stage).Inc()
}

// RecordPackagingJob records a packaging job completion.
func RecordPackagingJob(status, format string, duration float64) {
	PackagingJobsTotal.WithLabelValues(status, format).Inc()
	PackagingDuration.WithLabelValues(format).Observe(duration)
}

// RecordDRMKeyFetch records a DRM key fetch operation.
func RecordDRMKeyFetch(provider, status string, duration float64) {
	DRMKeyFetchTotal.WithLabelValues(provider, status).Inc()
	DRMKeyFetchDuration.WithLabelValues(provider).Observe(duration)
}

// RecordDRMEncryption records a DRM encryption operation.
func RecordDRMEncryption(system, scheme string, duration float64) {
	DRMEncryptionDuration.WithLabelValues(system, scheme).Observe(duration)
	DRMEncryptedVideos.WithLabelValues(system).Inc()
}

// UpdateCDNHealth updates CDN health metrics.
func UpdateCDNHealth(provider string, healthy bool, latency float64) {
	if healthy {
		CDNHealthStatus.WithLabelValues(provider).Set(1)
	} else {
		CDNHealthStatus.WithLabelValues(provider).Set(0)
	}
	CDNLatency.WithLabelValues(provider).Set(latency)
}

// RecordCDNRequest records a CDN request.
func RecordCDNRequest(provider, status string) {
	CDNRequestsTotal.WithLabelValues(provider, status).Inc()
}

// RecordCDNFailover records a CDN failover event.
func RecordCDNFailover(fromProvider, toProvider string) {
	CDNFailovers.WithLabelValues(fromProvider, toProvider).Inc()
}

// RecordHTTPRequest records an HTTP request.
func RecordHTTPRequest(method, endpoint, status string, duration float64, requestSize, responseSize int64) {
	HTTPRequestsTotal.WithLabelValues(method, endpoint, status).Inc()
	HTTPRequestDuration.WithLabelValues(method, endpoint).Observe(duration)
	HTTPRequestSize.WithLabelValues(method, endpoint).Observe(float64(requestSize))
	HTTPResponseSize.WithLabelValues(method, endpoint).Observe(float64(responseSize))
}

// RecordS3Operation records an S3 operation.
func RecordS3Operation(operation, bucket, status string, duration float64) {
	S3OperationsTotal.WithLabelValues(operation, bucket, status).Inc()
	S3OperationDuration.WithLabelValues(operation, bucket).Observe(duration)
}

// RecordS3Transfer records bytes transferred to/from S3.
func RecordS3Transfer(direction, bucket string, bytes int64) {
	S3BytesTransferred.WithLabelValues(direction, bucket).Add(float64(bytes))
}

