package live

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// StreamState represents the state of a live stream.
type StreamState string

const (
	StreamStateIdle       StreamState = "idle"
	StreamStateStarting   StreamState = "starting"
	StreamStateActive     StreamState = "active"
	StreamStateError      StreamState = "error"
	StreamStateStopping   StreamState = "stopping"
	StreamStateStopped    StreamState = "stopped"
)

// Stream represents a live ingest stream.
type Stream struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	State       StreamState       `json:"state"`
	InputType   InputType         `json:"inputType"`
	InputURL    string            `json:"inputUrl"`
	OutputURL   string            `json:"outputUrl,omitempty"`
	StartedAt   *time.Time        `json:"startedAt,omitempty"`
	StoppedAt   *time.Time        `json:"stoppedAt,omitempty"`
	Error       string            `json:"error,omitempty"`
	Stats       *StreamStats      `json:"stats,omitempty"`
	Config      *StreamConfig     `json:"config"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	
	// Internal fields
	ctx        context.Context
	cancel     context.CancelFunc
	packager   *LivePackager
	mu         sync.RWMutex
}

// InputType represents the type of input stream.
type InputType string

const (
	InputTypeSRT  InputType = "srt"
	InputTypeRTMP InputType = "rtmp"
	InputTypeRTSP InputType = "rtsp"
	InputTypeUDP  InputType = "udp"
)

// StreamConfig contains configuration for a live stream.
type StreamConfig struct {
	// Input settings
	InputPort       int    `json:"inputPort,omitempty"`
	InputPath       string `json:"inputPath,omitempty"` // For RTMP/RTSP
	InputMode       string `json:"inputMode,omitempty"` // "listener" or "caller" for SRT
	InputPassphrase string `json:"-"`                   // SRT encryption passphrase

	// Output settings
	OutputBucket    string   `json:"outputBucket"`
	OutputPrefix    string   `json:"outputPrefix"`
	SegmentDuration float64  `json:"segmentDuration"` // seconds
	PartDuration    float64  `json:"partDuration"`    // seconds (for LL-HLS)
	Presets         []string `json:"presets,omitempty"`

	// Feature flags
	EnableLLHLS     bool `json:"enableLlhls"`
	EnableDRM       bool `json:"enableDrm"`
	EnableSSAI      bool `json:"enableSsai"`

	// DVR / Catchup settings
	DVRWindowSize   int  `json:"dvrWindowSize,omitempty"` // segments to keep (0 = disabled)
	EnableRecording bool `json:"enableRecording"`

	// Failover settings
	EnableRedundancy bool   `json:"enableRedundancy"`
	BackupInputURL   string `json:"backupInputUrl,omitempty"`
}

// StreamStats contains statistics for a live stream.
type StreamStats struct {
	BytesReceived    int64         `json:"bytesReceived"`
	BytesSent        int64         `json:"bytesSent"`
	FramesReceived   int64         `json:"framesReceived"`
	FramesDropped    int64         `json:"framesDropped"`
	Bitrate          int64         `json:"bitrate"` // bps
	Duration         time.Duration `json:"duration"`
	SegmentsCreated  int64         `json:"segmentsCreated"`
	PartsCreated     int64         `json:"partsCreated"`
	LastSegmentTime  time.Time     `json:"lastSegmentTime"`
	CurrentLatency   float64       `json:"currentLatency"` // seconds
	InputResolution  string        `json:"inputResolution,omitempty"`
	InputCodec       string        `json:"inputCodec,omitempty"`
	InputFrameRate   float64       `json:"inputFrameRate,omitempty"`
}

// StreamManager manages live streams.
type StreamManager struct {
	streams   map[string]*Stream
	mu        sync.RWMutex
	logger    *slog.Logger
	config    *ManagerConfig
	
	// Callbacks
	onStreamStart func(stream *Stream)
	onStreamStop  func(stream *Stream)
	onStreamError func(stream *Stream, err error)
}

// ManagerConfig contains configuration for the stream manager.
type ManagerConfig struct {
	MaxStreams         int           `json:"maxStreams"`
	DefaultPresets     []string      `json:"defaultPresets"`
	OutputBucket       string        `json:"outputBucket"`
	OutputPrefix       string        `json:"outputPrefix"`
	HealthCheckInterval time.Duration `json:"healthCheckInterval"`
	StatsInterval      time.Duration `json:"statsInterval"`
	
	// MediaMTX settings
	MediaMTXURL        string `json:"mediamtxUrl"`
	MediaMTXAPIPort    int    `json:"mediamtxApiPort"`
	
	// AWS settings
	AWSRegion          string `json:"awsRegion"`
	CDNDomain          string `json:"cdnDomain,omitempty"`
}

// NewStreamManager creates a new stream manager.
func NewStreamManager(cfg *ManagerConfig, logger *slog.Logger) *StreamManager {
	if cfg.HealthCheckInterval == 0 {
		cfg.HealthCheckInterval = 10 * time.Second
	}
	if cfg.StatsInterval == 0 {
		cfg.StatsInterval = 5 * time.Second
	}
	if cfg.MaxStreams == 0 {
		cfg.MaxStreams = 10
	}

	return &StreamManager{
		streams: make(map[string]*Stream),
		logger:  logger,
		config:  cfg,
	}
}

// CreateStream creates a new live stream.
func (m *StreamManager) CreateStream(ctx context.Context, name string, inputType InputType, cfg *StreamConfig) (*Stream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check max streams
	if len(m.streams) >= m.config.MaxStreams {
		return nil, fmt.Errorf("maximum number of streams (%d) reached", m.config.MaxStreams)
	}

	// Generate stream ID
	streamID := uuid.New().String()[:8]

	// Apply defaults
	if cfg.OutputBucket == "" {
		cfg.OutputBucket = m.config.OutputBucket
	}
	if cfg.OutputPrefix == "" {
		cfg.OutputPrefix = fmt.Sprintf("%s/live/%s", m.config.OutputPrefix, streamID)
	}
	if len(cfg.Presets) == 0 {
		cfg.Presets = m.config.DefaultPresets
	}
	if cfg.SegmentDuration == 0 {
		cfg.SegmentDuration = 4.0
	}
	if cfg.PartDuration == 0 && cfg.EnableLLHLS {
		cfg.PartDuration = 0.5
	}

	// Build input URL based on type
	inputURL := buildInputURL(inputType, cfg, m.config)

	stream := &Stream{
		ID:        streamID,
		Name:      name,
		State:     StreamStateIdle,
		InputType: inputType,
		InputURL:  inputURL,
		Config:    cfg,
		Stats:     &StreamStats{},
		Metadata:  make(map[string]string),
	}

	m.streams[streamID] = stream

	m.logger.Info("stream created",
		"streamId", streamID,
		"name", name,
		"inputType", inputType,
	)

	return stream, nil
}

// StartStream starts a live stream.
func (m *StreamManager) StartStream(ctx context.Context, streamID string) error {
	m.mu.Lock()
	stream, exists := m.streams[streamID]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("stream not found: %s", streamID)
	}
	m.mu.Unlock()

	stream.mu.Lock()
	if stream.State != StreamStateIdle && stream.State != StreamStateStopped {
		stream.mu.Unlock()
		return fmt.Errorf("stream cannot be started in state: %s", stream.State)
	}
	stream.State = StreamStateStarting
	stream.mu.Unlock()

	// Create cancellable context for the stream
	streamCtx, cancel := context.WithCancel(ctx)
	stream.ctx = streamCtx
	stream.cancel = cancel

	// Create and start the packager
	packager, err := NewLivePackager(&LivePackagerConfig{
		StreamID:        streamID,
		InputURL:        stream.InputURL,
		OutputBucket:    stream.Config.OutputBucket,
		OutputPrefix:    stream.Config.OutputPrefix,
		SegmentDuration: stream.Config.SegmentDuration,
		PartDuration:    stream.Config.PartDuration,
		Presets:         stream.Config.Presets,
		EnableLLHLS:     stream.Config.EnableLLHLS,
		EnableDRM:       stream.Config.EnableDRM,
		DVRWindowSize:   stream.Config.DVRWindowSize,
		AWSRegion:       m.config.AWSRegion,
	}, m.logger)
	if err != nil {
		stream.mu.Lock()
		stream.State = StreamStateError
		stream.Error = err.Error()
		stream.mu.Unlock()
		return fmt.Errorf("failed to create packager: %w", err)
	}

	stream.packager = packager

	// Start the packager in a goroutine
	go func() {
		if err := packager.Start(streamCtx); err != nil {
			m.logger.Error("packager error",
				"streamId", streamID,
				"error", err,
			)
			stream.mu.Lock()
			stream.State = StreamStateError
			stream.Error = err.Error()
			stream.mu.Unlock()

			if m.onStreamError != nil {
				m.onStreamError(stream, err)
			}
		}
	}()

	// Wait for stream to become active (or timeout)
	startTimeout := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-startTimeout:
			cancel()
			return fmt.Errorf("stream start timeout")
		case <-ticker.C:
			if packager.IsActive() {
				now := time.Now()
				stream.mu.Lock()
				stream.State = StreamStateActive
				stream.StartedAt = &now
				stream.OutputURL = buildOutputURL(stream.Config, m.config)
				stream.mu.Unlock()

				m.logger.Info("stream started",
					"streamId", streamID,
					"outputUrl", stream.OutputURL,
				)

				if m.onStreamStart != nil {
					m.onStreamStart(stream)
				}

				// Start stats collection
				go m.collectStats(streamCtx, stream)

				return nil
			}
		case <-streamCtx.Done():
			return streamCtx.Err()
		}
	}
}

// StopStream stops a live stream.
func (m *StreamManager) StopStream(ctx context.Context, streamID string) error {
	m.mu.RLock()
	stream, exists := m.streams[streamID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("stream not found: %s", streamID)
	}

	stream.mu.Lock()
	if stream.State != StreamStateActive {
		stream.mu.Unlock()
		return fmt.Errorf("stream is not active: %s", stream.State)
	}
	stream.State = StreamStateStopping
	stream.mu.Unlock()

	// Cancel the stream context
	if stream.cancel != nil {
		stream.cancel()
	}

	// Stop the packager
	if stream.packager != nil {
		if err := stream.packager.Stop(ctx); err != nil {
			m.logger.Warn("error stopping packager",
				"streamId", streamID,
				"error", err,
			)
		}
	}

	now := time.Now()
	stream.mu.Lock()
	stream.State = StreamStateStopped
	stream.StoppedAt = &now
	stream.mu.Unlock()

	m.logger.Info("stream stopped",
		"streamId", streamID,
	)

	if m.onStreamStop != nil {
		m.onStreamStop(stream)
	}

	return nil
}

// GetStream returns a stream by ID.
func (m *StreamManager) GetStream(streamID string) (*Stream, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stream, exists := m.streams[streamID]
	if !exists {
		return nil, fmt.Errorf("stream not found: %s", streamID)
	}

	return stream, nil
}

// ListStreams returns all streams.
func (m *StreamManager) ListStreams() []*Stream {
	m.mu.RLock()
	defer m.mu.RUnlock()

	streams := make([]*Stream, 0, len(m.streams))
	for _, s := range m.streams {
		streams = append(streams, s)
	}

	return streams
}

// DeleteStream deletes a stream.
func (m *StreamManager) DeleteStream(ctx context.Context, streamID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	stream, exists := m.streams[streamID]
	if !exists {
		return fmt.Errorf("stream not found: %s", streamID)
	}

	// Stop if active
	if stream.State == StreamStateActive {
		m.mu.Unlock()
		if err := m.StopStream(ctx, streamID); err != nil {
			m.logger.Warn("error stopping stream during delete",
				"streamId", streamID,
				"error", err,
			)
		}
		m.mu.Lock()
	}

	delete(m.streams, streamID)

	m.logger.Info("stream deleted",
		"streamId", streamID,
	)

	return nil
}

// collectStats periodically collects and updates stream statistics.
func (m *StreamManager) collectStats(ctx context.Context, stream *Stream) {
	ticker := time.NewTicker(m.config.StatsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if stream.packager == nil {
				continue
			}

			stats := stream.packager.GetStats()
			stream.mu.Lock()
			stream.Stats = stats
			stream.mu.Unlock()
		}
	}
}

// buildInputURL constructs the input URL based on input type and config.
func buildInputURL(inputType InputType, cfg *StreamConfig, mgrCfg *ManagerConfig) string {
	switch inputType {
	case InputTypeSRT:
		mode := cfg.InputMode
		if mode == "" {
			mode = "listener"
		}
		url := fmt.Sprintf("srt://0.0.0.0:%d?mode=%s", cfg.InputPort, mode)
		if cfg.InputPassphrase != "" {
			url += "&passphrase=" + cfg.InputPassphrase
		}
		return url

	case InputTypeRTMP:
		path := cfg.InputPath
		if path == "" {
			path = "/live"
		}
		return fmt.Sprintf("rtmp://%s%s", mgrCfg.MediaMTXURL, path)

	case InputTypeRTSP:
		path := cfg.InputPath
		if path == "" {
			path = "/live"
		}
		return fmt.Sprintf("rtsp://%s%s", mgrCfg.MediaMTXURL, path)

	case InputTypeUDP:
		return fmt.Sprintf("udp://0.0.0.0:%d", cfg.InputPort)

	default:
		return ""
	}
}

// buildOutputURL constructs the output HLS URL.
func buildOutputURL(cfg *StreamConfig, mgrCfg *ManagerConfig) string {
	if mgrCfg.CDNDomain != "" {
		return fmt.Sprintf("https://%s/%s/master.m3u8", mgrCfg.CDNDomain, cfg.OutputPrefix)
	}
	return fmt.Sprintf("s3://%s/%s/master.m3u8", cfg.OutputBucket, cfg.OutputPrefix)
}

// OnStreamStart sets the callback for stream start events.
func (m *StreamManager) OnStreamStart(fn func(stream *Stream)) {
	m.onStreamStart = fn
}

// OnStreamStop sets the callback for stream stop events.
func (m *StreamManager) OnStreamStop(fn func(stream *Stream)) {
	m.onStreamStop = fn
}

// OnStreamError sets the callback for stream error events.
func (m *StreamManager) OnStreamError(fn func(stream *Stream, err error)) {
	m.onStreamError = fn
}

