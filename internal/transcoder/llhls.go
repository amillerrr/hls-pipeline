package transcoder

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("hls-transcoder")

// LLHLSConfig contains configuration for LL-HLS transcoding.
type LLHLSConfig struct {
	// PartDuration is the duration of each partial segment in seconds (0.5-1.0 recommended)
	PartDuration float64 `json:"partDuration"`

	// SegmentDuration is the duration of each full segment in seconds (4-6 recommended)
	SegmentDuration float64 `json:"segmentDuration"`

	// PartHoldBack is the minimum time to hold back partial segments (3 * PartDuration)
	PartHoldBack float64 `json:"partHoldBack"`

	// CanBlockReload enables blocking playlist reload (CAN-BLOCK-RELOAD)
	CanBlockReload bool `json:"canBlockReload"`

	// CanSkipUntil is the duration for delta playlist updates (EXT-X-SKIP)
	CanSkipUntil float64 `json:"canSkipUntil"`

	// UseCMAF enables CMAF (fMP4) segments instead of MPEG-TS
	UseCMAF bool `json:"useCmaf"`

	// GOPSize is the number of frames per GOP (keyframe interval)
	GOPSize int `json:"gopSize"`

	// FrameRate is the output frame rate
	FrameRate float64 `json:"frameRate"`

	// EnableIFramePlaylist enables I-frame playlist generation for trick play
	EnableIFramePlaylist bool `json:"enableIFramePlaylist"`

	// HLSVersion is the HLS playlist version (9 for LL-HLS)
	HLSVersion int `json:"hlsVersion"`

	// Presets defines the encoding ladder
	Presets []TranscodePreset `json:"presets"`
}

// TranscodePreset defines a single encoding preset.
type TranscodePreset struct {
	Name         string `json:"name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	VideoBitrate string `json:"videoBitrate"`
	AudioBitrate string `json:"audioBitrate"`
	Profile      string `json:"profile"`
	Level        string `json:"level"`
}

// DefaultLLHLSConfig returns the default LL-HLS configuration.
func DefaultLLHLSConfig() *LLHLSConfig {
	return &LLHLSConfig{
		PartDuration:         0.5,
		SegmentDuration:      4.0,
		PartHoldBack:         1.5, // 3 * PartDuration
		CanBlockReload:       true,
		CanSkipUntil:         12.0, // 3 * SegmentDuration
		UseCMAF:              true,
		GOPSize:              48,   // 2 seconds at 24fps
		FrameRate:            24.0,
		EnableIFramePlaylist: true,
		HLSVersion:           9,
		Presets:              DefaultPresets(),
	}
}

// DefaultPresets returns the default encoding ladder.
func DefaultPresets() []TranscodePreset {
	return []TranscodePreset{
		{Name: "1080p", Width: 1920, Height: 1080, VideoBitrate: "5000k", AudioBitrate: "192k", Profile: "high", Level: "4.2"},
		{Name: "720p", Width: 1280, Height: 720, VideoBitrate: "2800k", AudioBitrate: "128k", Profile: "high", Level: "4.1"},
		{Name: "480p", Width: 854, Height: 480, VideoBitrate: "1400k", AudioBitrate: "128k", Profile: "main", Level: "3.1"},
		{Name: "360p", Width: 640, Height: 360, VideoBitrate: "800k", AudioBitrate: "96k", Profile: "main", Level: "3.0"},
		{Name: "240p", Width: 426, Height: 240, VideoBitrate: "400k", AudioBitrate: "64k", Profile: "baseline", Level: "3.0"},
	}
}

// LLHLSTranscoder handles LL-HLS transcoding.
type LLHLSTranscoder struct {
	config     *LLHLSConfig
	ffmpegPath string
	logger     *slog.Logger
}

// NewLLHLSTranscoder creates a new LL-HLS transcoder.
func NewLLHLSTranscoder(config *LLHLSConfig, logger *slog.Logger) (*LLHLSTranscoder, error) {
	if config == nil {
		config = DefaultLLHLSConfig()
	}

	// Find FFmpeg binary
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}

	return &LLHLSTranscoder{
		config:     config,
		ffmpegPath: ffmpegPath,
		logger:     logger,
	}, nil
}

// TranscodeResult contains the results of a transcoding operation.
type TranscodeResult struct {
	MasterPlaylistPath string            `json:"masterPlaylistPath"`
	VariantPlaylists   []string          `json:"variantPlaylists"`
	IFramePlaylists    []string          `json:"iframePlaylists"`
	InitSegments       []string          `json:"initSegments"`
	Duration           time.Duration     `json:"duration"`
	OutputSize         int64             `json:"outputSize"`
	Metadata           map[string]string `json:"metadata"`
}

// Transcode performs LL-HLS transcoding on the input file.
func (t *LLHLSTranscoder) Transcode(ctx context.Context, inputPath, outputDir string) (*TranscodeResult, error) {
	ctx, span := tracer.Start(ctx, "llhls-transcode",
		trace.WithAttributes(
			attribute.String("input.path", inputPath),
			attribute.String("output.dir", outputDir),
			attribute.Bool("cmaf.enabled", t.config.UseCMAF),
		))
	defer span.End()

	startTime := time.Now()

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Probe input file
	metadata, err := t.probeInput(ctx, inputPath)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to probe input: %w", err)
	}

	span.SetAttributes(
		attribute.String("input.duration", metadata["duration"]),
		attribute.String("input.video_codec", metadata["video_codec"]),
	)

	// Build and execute FFmpeg command
	args := t.buildFFmpegArgs(inputPath, outputDir)
	
	t.logger.InfoContext(ctx, "Starting LL-HLS transcoding",
		"input", inputPath,
		"output", outputDir,
		"presets", len(t.config.Presets),
	)

	cmd := exec.CommandContext(ctx, t.ffmpegPath, args...)
	
	// Capture stderr for progress monitoring
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	// Monitor progress
	go t.monitorProgress(ctx, stderr)

	if err := cmd.Wait(); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("FFmpeg transcoding failed: %w", err)
	}

	// Generate master playlist
	masterPath := filepath.Join(outputDir, "master.m3u8")
	if err := t.generateMasterPlaylist(ctx, outputDir, masterPath); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to generate master playlist: %w", err)
	}

	// Calculate output size
	outputSize, err := calculateDirSize(outputDir)
	if err != nil {
		t.logger.WarnContext(ctx, "Failed to calculate output size", "error", err)
	}

	result := &TranscodeResult{
		MasterPlaylistPath: masterPath,
		VariantPlaylists:   t.getVariantPlaylists(outputDir),
		IFramePlaylists:    t.getIFramePlaylists(outputDir),
		InitSegments:       t.getInitSegments(outputDir),
		Duration:           time.Since(startTime),
		OutputSize:         outputSize,
		Metadata:           metadata,
	}

	span.SetAttributes(
		attribute.Int64("output.size_bytes", outputSize),
		attribute.Int64("transcode.duration_ms", result.Duration.Milliseconds()),
	)

	t.logger.InfoContext(ctx, "LL-HLS transcoding complete",
		"duration", result.Duration,
		"outputSize", outputSize,
		"variants", len(result.VariantPlaylists),
	)

	return result, nil
}

// buildFFmpegArgs builds the FFmpeg command line arguments.
func (t *LLHLSTranscoder) buildFFmpegArgs(inputPath, outputDir string) []string {
	args := []string{
		"-i", inputPath,
		"-y", // Overwrite output files
	}

	// Video filter for scaling
	var filterComplex strings.Builder
	var mapArgs []string

	for i, preset := range t.config.Presets {
		// Scale filter
		filterComplex.WriteString(fmt.Sprintf("[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:-1:-1[v%d];",
			preset.Width, preset.Height, preset.Width, preset.Height, i))

		mapArgs = append(mapArgs, "-map", fmt.Sprintf("[v%d]", i))
		mapArgs = append(mapArgs, "-map", "0:a?")
	}

	if filterComplex.Len() > 0 {
		args = append(args, "-filter_complex", strings.TrimSuffix(filterComplex.String(), ";"))
	}

	args = append(args, mapArgs...)

	// Per-stream encoding options
	for i, preset := range t.config.Presets {
		vidx := i * 2
		aidx := vidx + 1

		// Video encoding
		args = append(args,
			fmt.Sprintf("-c:v:%d", vidx), "libx264",
			fmt.Sprintf("-b:v:%d", vidx), preset.VideoBitrate,
			fmt.Sprintf("-profile:v:%d", vidx), preset.Profile,
			fmt.Sprintf("-level:v:%d", vidx), preset.Level,
			fmt.Sprintf("-preset:%d", vidx), "fast",
			fmt.Sprintf("-g:%d", vidx), strconv.Itoa(t.config.GOPSize),
			fmt.Sprintf("-keyint_min:%d", vidx), strconv.Itoa(t.config.GOPSize),
			fmt.Sprintf("-sc_threshold:%d", vidx), "0",
		)

		// Audio encoding
		args = append(args,
			fmt.Sprintf("-c:a:%d", aidx), "aac",
			fmt.Sprintf("-b:a:%d", aidx), preset.AudioBitrate,
			fmt.Sprintf("-ac:%d", aidx), "2",
			fmt.Sprintf("-ar:%d", aidx), "48000",
		)
	}

	// HLS options
	args = append(args,
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%.1f", t.config.SegmentDuration),
		"-hls_list_size", "0",
		"-hls_playlist_type", "vod",
	)

	// CMAF / fMP4 segments
	if t.config.UseCMAF {
		args = append(args,
			"-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", "init_%v.mp4",
		)
	}

	// LL-HLS specific options
	args = append(args,
		"-hls_flags", "independent_segments+program_date_time+single_file",
	)

	// Segment filename pattern
	segExt := ".ts"
	if t.config.UseCMAF {
		segExt = ".m4s"
	}
	args = append(args,
		"-hls_segment_filename", filepath.Join(outputDir, "%v", "seg_%03d"+segExt),
	)

	// Variant stream map
	var varStreamMap strings.Builder
	for i, preset := range t.config.Presets {
		if i > 0 {
			varStreamMap.WriteString(" ")
		}
		varStreamMap.WriteString(fmt.Sprintf("v:%d,a:%d,name:%s", i, i, preset.Name))
	}
	args = append(args, "-var_stream_map", varStreamMap.String())

	// Master playlist output
	args = append(args,
		"-master_pl_name", "master.m3u8",
		filepath.Join(outputDir, "%v", "playlist.m3u8"),
	)

	return args
}

// probeInput probes the input file for metadata.
func (t *LLHLSTranscoder) probeInput(ctx context.Context, inputPath string) (map[string]string, error) {
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, fmt.Errorf("ffprobe not found: %w", err)
	}

	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		inputPath,
	}

	cmd := exec.CommandContext(ctx, ffprobePath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	// Parse basic metadata
	metadata := make(map[string]string)
	
	// Extract duration using regex (simple approach)
	durationRe := regexp.MustCompile(`"duration"\s*:\s*"([^"]+)"`)
	if matches := durationRe.FindSubmatch(output); len(matches) > 1 {
		metadata["duration"] = string(matches[1])
	}

	// Extract video codec
	codecRe := regexp.MustCompile(`"codec_name"\s*:\s*"(h264|hevc|vp9|av1)"`)
	if matches := codecRe.FindSubmatch(output); len(matches) > 1 {
		metadata["video_codec"] = string(matches[1])
	}

	// Extract dimensions
	widthRe := regexp.MustCompile(`"width"\s*:\s*(\d+)`)
	if matches := widthRe.FindSubmatch(output); len(matches) > 1 {
		metadata["width"] = string(matches[1])
	}

	heightRe := regexp.MustCompile(`"height"\s*:\s*(\d+)`)
	if matches := heightRe.FindSubmatch(output); len(matches) > 1 {
		metadata["height"] = string(matches[1])
	}

	return metadata, nil
}

// monitorProgress monitors FFmpeg transcoding progress.
func (t *LLHLSTranscoder) monitorProgress(ctx context.Context, stderr *os.File) {
	scanner := bufio.NewScanner(stderr)
	progressRe := regexp.MustCompile(`time=(\d+:\d+:\d+\.\d+)`)

	for scanner.Scan() {
		line := scanner.Text()
		if matches := progressRe.FindStringSubmatch(line); len(matches) > 1 {
			t.logger.DebugContext(ctx, "Transcoding progress", "time", matches[1])
		}
	}
}

// generateMasterPlaylist generates an HLS v9 master playlist.
func (t *LLHLSTranscoder) generateMasterPlaylist(ctx context.Context, outputDir, masterPath string) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString(fmt.Sprintf("#EXT-X-VERSION:%d\n", t.config.HLSVersion))
	playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")

	// LL-HLS server control
	if t.config.CanBlockReload {
		playlist.WriteString(fmt.Sprintf("#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=%.1f,CAN-SKIP-UNTIL=%.1f\n",
			t.config.PartHoldBack, t.config.CanSkipUntil))
	}

	playlist.WriteString("\n")

	// Variant streams
	for _, preset := range t.config.Presets {
		bandwidth := parseBitrate(preset.VideoBitrate) + parseBitrate(preset.AudioBitrate)
		avgBandwidth := bandwidth * 9 / 10 // 90% of peak

		playlist.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d,RESOLUTION=%dx%d,FRAME-RATE=%.3f,CODECS=\"avc1.%s,mp4a.40.2\",AUDIO=\"audio\"\n",
			bandwidth,
			avgBandwidth,
			preset.Width,
			preset.Height,
			t.config.FrameRate,
			getAVCCodecString(preset.Profile, preset.Level),
		))
		playlist.WriteString(fmt.Sprintf("%s/playlist.m3u8\n", preset.Name))
	}

	// I-Frame playlists for trick play
	if t.config.EnableIFramePlaylist {
		playlist.WriteString("\n")
		for _, preset := range t.config.Presets {
			bandwidth := parseBitrate(preset.VideoBitrate) / 10 // I-frame only bandwidth estimate
			playlist.WriteString(fmt.Sprintf("#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"avc1.%s\",URI=\"%s/iframe.m3u8\"\n",
				bandwidth,
				preset.Width,
				preset.Height,
				getAVCCodecString(preset.Profile, preset.Level),
				preset.Name,
			))
		}
	}

	// Audio group
	playlist.WriteString("\n")
	playlist.WriteString("#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"English\",LANGUAGE=\"en\",DEFAULT=YES,AUTOSELECT=YES\n")

	return os.WriteFile(masterPath, []byte(playlist.String()), 0644)
}

// getVariantPlaylists returns paths to all variant playlists.
func (t *LLHLSTranscoder) getVariantPlaylists(outputDir string) []string {
	var playlists []string
	for _, preset := range t.config.Presets {
		playlists = append(playlists, filepath.Join(outputDir, preset.Name, "playlist.m3u8"))
	}
	return playlists
}

// getIFramePlaylists returns paths to all I-Frame playlists.
func (t *LLHLSTranscoder) getIFramePlaylists(outputDir string) []string {
	if !t.config.EnableIFramePlaylist {
		return nil
	}
	var playlists []string
	for _, preset := range t.config.Presets {
		playlists = append(playlists, filepath.Join(outputDir, preset.Name, "iframe.m3u8"))
	}
	return playlists
}

// getInitSegments returns paths to all init segments.
func (t *LLHLSTranscoder) getInitSegments(outputDir string) []string {
	if !t.config.UseCMAF {
		return nil
	}
	var segments []string
	for _, preset := range t.config.Presets {
		segments = append(segments, filepath.Join(outputDir, preset.Name, fmt.Sprintf("init_%s.mp4", preset.Name)))
	}
	return segments
}

// Helper functions

func parseBitrate(bitrate string) int {
	bitrate = strings.ToLower(strings.TrimSpace(bitrate))
	multiplier := 1

	if strings.HasSuffix(bitrate, "k") {
		multiplier = 1000
		bitrate = strings.TrimSuffix(bitrate, "k")
	} else if strings.HasSuffix(bitrate, "m") {
		multiplier = 1000000
		bitrate = strings.TrimSuffix(bitrate, "m")
	}

	value, _ := strconv.Atoi(bitrate)
	return value * multiplier
}

func getAVCCodecString(profile, level string) string {
	profileHex := map[string]string{
		"baseline":    "42",
		"main":        "4D",
		"high":        "64",
		"high10":      "6E",
		"high422":     "7A",
		"high444":     "F4",
	}

	levelHex := map[string]string{
		"3.0": "1E",
		"3.1": "1F",
		"4.0": "28",
		"4.1": "29",
		"4.2": "2A",
		"5.0": "32",
		"5.1": "33",
	}

	p := profileHex[strings.ToLower(profile)]
	if p == "" {
		p = "64" // Default to high
	}

	l := levelHex[level]
	if l == "" {
		l = "29" // Default to 4.1
	}

	return p + "00" + l
}

func calculateDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}
