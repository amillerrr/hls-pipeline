package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Live streaming metrics
var (
	// Stream lifecycle metrics
	LiveStreamsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "live_streams_active",
		Help: "Number of currently active live streams",
	})

	LiveStreamsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "live_streams_total",
		Help: "Total number of live streams created",
	}, []string{"input_type"})

	LiveStreamDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "live_stream_duration_seconds",
		Help:    "Duration of live streams in seconds",
		Buckets: []float64{60, 300, 900, 1800, 3600, 7200, 14400, 28800},
	}, []string{"input_type"})

	LiveStreamErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "live_stream_errors_total",
		Help: "Total number of live stream errors",
	}, []string{"input_type", "error_type"})

	// Ingest metrics
	LiveIngestBytesReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "live_ingest_bytes_received_total",
		Help: "Total bytes received from live ingest",
	}, []string{"stream_id", "input_type"})

	LiveIngestFramesReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "live_ingest_frames_received_total",
		Help: "Total frames received from live ingest",
	}, []string{"stream_id"})

	LiveIngestFramesDropped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "live_ingest_frames_dropped_total",
		Help: "Total frames dropped during live ingest",
	}, []string{"stream_id"})

	LiveIngestBitrate = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "live_ingest_bitrate_bps",
		Help: "Current ingest bitrate in bits per second",
	}, []string{"stream_id"})

	// Packaging metrics
	LiveSegmentsCreated = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "live_segments_created_total",
		Help: "Total number of segments created",
	}, []string{"stream_id", "variant"})

	LiveSegmentDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "live_segment_duration_seconds",
		Help:    "Duration of live segments",
		Buckets: []float64{0.5, 1, 2, 4, 6, 8, 10},
	}, []string{"stream_id"})

	LivePackagingLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "live_packaging_latency_seconds",
		Help:    "Latency from ingest to segment availability",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10},
	}, []string{"stream_id"})

	// Upload metrics
	LiveUploadDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "live_upload_duration_seconds",
		Help:    "Time to upload segments to S3",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5},
	}, []string{"stream_id", "file_type"})

	LiveUploadErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "live_upload_errors_total",
		Help: "Total number of segment upload errors",
	}, []string{"stream_id", "error_type"})

	// SRT-specific metrics
	SRTConnectionsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "srt_connections_active",
		Help: "Number of active SRT connections",
	})

	SRTPacketsReceived = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "srt_packets_received_total",
		Help: "Total SRT packets received",
	}, []string{"stream_id"})

	SRTPacketsLost = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "srt_packets_lost_total",
		Help: "Total SRT packets lost",
	}, []string{"stream_id"})

	SRTLatency = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "srt_latency_ms",
		Help: "Current SRT latency in milliseconds",
	}, []string{"stream_id"})

	SRTRoundTripTime = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "srt_rtt_ms",
		Help: "Current SRT round trip time in milliseconds",
	}, []string{"stream_id"})

	// MediaMTX metrics
	MediaMTXPathsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mediamtx_paths_active",
		Help: "Number of active paths in MediaMTX",
	})

	MediaMTXReadersActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "mediamtx_readers_active",
		Help: "Number of active readers per path",
	}, []string{"path"})
)

// RecordStreamStart records the start of a live stream.
func RecordStreamStart(inputType string) {
	LiveStreamsActive.Inc()
	LiveStreamsTotal.WithLabelValues(inputType).Inc()
}

// RecordStreamStop records the stop of a live stream.
func RecordStreamStop(inputType string, durationSeconds float64) {
	LiveStreamsActive.Dec()
	LiveStreamDuration.WithLabelValues(inputType).Observe(durationSeconds)
}

// RecordStreamError records a live stream error.
func RecordStreamError(inputType, errorType string) {
	LiveStreamErrors.WithLabelValues(inputType, errorType).Inc()
}

// RecordIngestStats records ingest statistics.
func RecordIngestStats(streamID, inputType string, bytesReceived, framesReceived, framesDropped int64, bitrate float64) {
	LiveIngestBytesReceived.WithLabelValues(streamID, inputType).Add(float64(bytesReceived))
	LiveIngestFramesReceived.WithLabelValues(streamID).Add(float64(framesReceived))
	LiveIngestFramesDropped.WithLabelValues(streamID).Add(float64(framesDropped))
	LiveIngestBitrate.WithLabelValues(streamID).Set(bitrate)
}

// RecordSegmentCreated records a segment creation.
func RecordSegmentCreated(streamID, variant string, duration float64) {
	LiveSegmentsCreated.WithLabelValues(streamID, variant).Inc()
	LiveSegmentDuration.WithLabelValues(streamID).Observe(duration)
}

// RecordPackagingLatency records the packaging latency.
func RecordPackagingLatency(streamID string, latency float64) {
	LivePackagingLatency.WithLabelValues(streamID).Observe(latency)
}

// RecordUpload records a segment upload.
func RecordUpload(streamID, fileType string, duration float64) {
	LiveUploadDuration.WithLabelValues(streamID, fileType).Observe(duration)
}

// RecordUploadError records an upload error.
func RecordUploadError(streamID, errorType string) {
	LiveUploadErrors.WithLabelValues(streamID, errorType).Inc()
}

// RecordSRTStats records SRT-specific statistics.
func RecordSRTStats(streamID string, packetsReceived, packetsLost int64, latencyMs, rttMs float64) {
	SRTPacketsReceived.WithLabelValues(streamID).Add(float64(packetsReceived))
	SRTPacketsLost.WithLabelValues(streamID).Add(float64(packetsLost))
	SRTLatency.WithLabelValues(streamID).Set(latencyMs)
	SRTRoundTripTime.WithLabelValues(streamID).Set(rttMs)
}

