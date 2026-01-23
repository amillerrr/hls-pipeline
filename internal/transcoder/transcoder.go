package transcoder

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/amillerrr/hls-pipeline/internal/config"
)

// TranscodeMode defines the transcoding output format.
type TranscodeMode string

const (
	// ModeStandardHLS produces standard HLS output.
	ModeStandardHLS TranscodeMode = "hls"
	// ModeLLHLS produces Low-Latency HLS output.
	ModeLLHLS TranscodeMode = "llhls"
	// ModeCMAF produces CMAF output (HLS + DASH).
	ModeCMAF TranscodeMode = "cmaf"
)

// TranscodeInput contains the input parameters for transcoding.
type TranscodeInput struct {
	VideoID      string            `json:"videoId"`
	SourceBucket string            `json:"sourceBucket"`
	SourceKey    string            `json:"sourceKey"`
	InputPath    string            `json:"inputPath"`
	OutputBucket string            `json:"outputBucket"`
	OutputPrefix string            `json:"outputPrefix"`
	OutputDir    string            `json:"outputDir"`
	Presets      []string          `json:"presets,omitempty"`
	Mode         TranscodeMode     `json:"mode"`
	Options      *TranscodeOptions `json:"options,omitempty"`
}

// TranscodeOptions contains optional transcoding parameters.
type TranscodeOptions struct {
	SegmentDuration      float64              `json:"segmentDuration,omitempty"`
	PartDuration         float64              `json:"partDuration,omitempty"`
	EnableIFramePlaylist bool                 `json:"enableIFramePlaylist,omitempty"`
	EnableDRM            bool                 `json:"enableDrm,omitempty"`
	DRMConfig            *DRMEncryptionConfig `json:"drmConfig,omitempty"`
	GenerateDASH         bool                 `json:"generateDash,omitempty"`
	GenerateHLS          bool                 `json:"generateHls,omitempty"`
}

// TranscodeOutput contains the results of a transcoding operation.
type TranscodeOutput struct {
	MasterPlaylistPath string           `json:"masterPlaylistPath"`
	DASHManifestPath   string           `json:"dashManifestPath,omitempty"`
	Variants           []VariantInfo    `json:"variants"`
	Duration           time.Duration    `json:"duration"`
	OutputSize         int64            `json:"outputSize"`
	Width              int              `json:"width"`
	Height             int              `json:"height"`
	Encrypted          bool             `json:"encrypted"`
	KeyID              string           `json:"keyId,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// VariantInfo contains information about a transcoded variant.
type VariantInfo struct {
	Name         string  `json:"name"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	Bandwidth    int64   `json:"bandwidth"`
	AvgBandwidth int64   `json:"avgBandwidth"`
	Codecs       string  `json:"codecs"`
	FrameRate    float64 `json:"frameRate"`
	PlaylistPath string  `json:"playlistPath"`
	InitSegment  string  `json:"initSegment,omitempty"`
}

// Transcoder is the interface for video transcoders.
type Transcoder interface {
	// Transcode performs transcoding on the input and returns the output.
	Transcode(ctx context.Context, input *TranscodeInput) (*TranscodeOutput, error)

	// GetPresets returns the configured presets.
	GetPresets() []config.PresetConfig

	// GetMode returns the transcoding mode.
	GetMode() TranscodeMode
}

// TranscoderConfig contains common configuration for all transcoders.
type TranscoderConfig struct {
	Mode            TranscodeMode        `json:"mode"`
	SegmentDuration float64              `json:"segmentDuration"`
	PartDuration    float64              `json:"partDuration"`
	FrameRate       float64              `json:"frameRate"`
	Presets         []config.PresetConfig `json:"presets"`
	Logger          *slog.Logger
}

// DefaultTranscoderConfig returns the default transcoder configuration.
func DefaultTranscoderConfig(mode TranscodeMode, logger *slog.Logger) *TranscoderConfig {
	cfg := &TranscoderConfig{
		Mode:            mode,
		SegmentDuration: 4.0,
		FrameRate:       30.0,
		Presets:         config.DefaultPresets(),
		Logger:          logger,
	}

	if mode == ModeLLHLS {
		cfg.PartDuration = 0.5
	}

	return cfg
}

// FFmpegExecutor handles FFmpeg command execution.
// This is shared by all transcoder implementations.
type FFmpegExecutor struct {
	ffmpegPath  string
	ffprobePath string
	logger      *slog.Logger
}

// NewFFmpegExecutor creates a new FFmpeg executor.
func NewFFmpegExecutor(logger *slog.Logger) (*FFmpegExecutor, error) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}

	ffprobePath, _ := exec.LookPath("ffprobe")

	return &FFmpegExecutor{
		ffmpegPath:  ffmpegPath,
		ffprobePath: ffprobePath,
		logger:      logger,
	}, nil
}

// Execute runs an FFmpeg command with the given arguments.
func (e *FFmpegExecutor) Execute(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, e.ffmpegPath, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		e.logger.ErrorContext(ctx, "FFmpeg execution failed",
			"args", args,
			"output", string(output),
			"error", err,
		)
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	return nil
}

// ExecuteWithProgress runs FFmpeg and monitors progress.
func (e *FFmpegExecutor) ExecuteWithProgress(ctx context.Context, args []string, progressFn func(float64)) error {
	cmd := exec.CommandContext(ctx, e.ffmpegPath, args...)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Monitor progress from stderr
	go monitorFFmpegProgress(ctx, stderr, progressFn, e.logger)

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("ffmpeg canceled: %w", ctx.Err())
		}
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	return nil
}

// Probe runs ffprobe on the input file and returns metadata.
func (e *FFmpegExecutor) Probe(ctx context.Context, inputPath string) (map[string]string, error) {
	if e.ffprobePath == "" {
		return nil, fmt.Errorf("ffprobe not available")
	}

	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		inputPath,
	}

	cmd := exec.CommandContext(ctx, e.ffprobePath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	return parseFFprobeOutput(output)
}

// NewTranscoderForMode creates a transcoder for the specified mode.
func NewTranscoderForMode(mode TranscodeMode, cfg *TranscoderConfig) (Transcoder, error) {
	if cfg == nil {
		cfg = DefaultTranscoderConfig(mode, slog.Default())
	}

	switch mode {
	case ModeStandardHLS:
		return NewStandardHLSTranscoder(cfg)
	case ModeLLHLS:
		return NewLLHLSTranscoderFromConfig(cfg)
	case ModeCMAF:
		return NewCMAFTranscoderFromConfig(cfg)
	default:
		return nil, fmt.Errorf("unsupported transcoding mode: %s", mode)
	}
}

// StandardHLSTranscoder implements standard HLS transcoding.
type StandardHLSTranscoder struct {
	config   *TranscoderConfig
	executor *FFmpegExecutor
}

// NewStandardHLSTranscoder creates a new standard HLS transcoder.
func NewStandardHLSTranscoder(cfg *TranscoderConfig) (*StandardHLSTranscoder, error) {
	executor, err := NewFFmpegExecutor(cfg.Logger)
	if err != nil {
		return nil, err
	}

	return &StandardHLSTranscoder{
		config:   cfg,
		executor: executor,
	}, nil
}

// Transcode implements the Transcoder interface for standard HLS.
func (t *StandardHLSTranscoder) Transcode(ctx context.Context, input *TranscodeInput) (*TranscodeOutput, error) {
	_, span := tracer.Start(ctx, "standard-hls-transcode")
	defer span.End()

	startTime := time.Now()

	builder := NewFFmpegArgsBuilder().
		Input(input.InputPath).
		Overwrite().
		VideoCodec("libx264").
		AudioCodec("aac").
		HLSOutput(input.OutputDir, t.config.SegmentDuration)

	// Add presets
	for _, preset := range t.config.Presets {
		builder.AddPreset(preset)
	}

	args := builder.Build()

	if err := t.executor.Execute(ctx, args); err != nil {
		return nil, fmt.Errorf("transcoding failed: %w", err)
	}

	return &TranscodeOutput{
		MasterPlaylistPath: input.OutputDir + "/master.m3u8",
		Duration:           time.Since(startTime),
	}, nil
}

// GetPresets returns the configured presets.
func (t *StandardHLSTranscoder) GetPresets() []config.PresetConfig {
	return t.config.Presets
}

// GetMode returns the transcoding mode.
func (t *StandardHLSTranscoder) GetMode() TranscodeMode {
	return ModeStandardHLS
}

// NewLLHLSTranscoderFromConfig creates an LLHLS transcoder from config.
func NewLLHLSTranscoderFromConfig(cfg *TranscoderConfig) (*LLHLSTranscoder, error) {
	llhlsCfg := &LLHLSConfig{
		PartDuration:         cfg.PartDuration,
		SegmentDuration:      cfg.SegmentDuration,
		PartHoldBack:         cfg.PartDuration * 3,
		CanBlockReload:       true,
		CanSkipUntil:         12.0,
		UseCMAF:              true,
		FrameRate:            cfg.FrameRate,
		EnableIFramePlaylist: true,
		Presets:              cfg.Presets,
	}

	return NewLLHLSTranscoder(llhlsCfg, cfg.Logger)
}

// NewCMAFTranscoderFromConfig creates a CMAF packager from config.
func NewCMAFTranscoderFromConfig(cfg *TranscoderConfig) (*CMAFPackager, error) {
	cmafCfg := &CMAFConfig{
		SegmentDuration:  cfg.SegmentDuration,
		FragmentDuration: cfg.PartDuration,
		EnableLowLatency: true,
		GenerateHLS:      true,
		GenerateDASH:     true,
		UseShaka:         false,
		Presets:          cfg.Presets,
	}

	return NewCMAFPackager(cmafCfg, cfg.Logger)
}
