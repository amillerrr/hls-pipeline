package transcoder

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/amillerrr/hls-pipeline/internal/config"
)

var tracer = otel.Tracer("hls-transcoder")

// LLHLSConfig contains configuration for LL-HLS transcoding.
type LLHLSConfig struct {
	PartDuration         float64              `json:"partDuration"`
	SegmentDuration      float64              `json:"segmentDuration"`
	PartHoldBack         float64              `json:"partHoldBack"`
	CanBlockReload       bool                 `json:"canBlockReload"`
	CanSkipUntil         float64              `json:"canSkipUntil"`
	UseCMAF              bool                 `json:"useCmaf"`
	FrameRate            float64              `json:"frameRate"`
	EnableIFramePlaylist bool                 `json:"enableIFramePlaylist"`
	Presets              []config.PresetConfig `json:"presets"`
	DRMConfig            *DRMEncryptionConfig `json:"drmConfig,omitempty"`
}

// DRMEncryptionConfig contains DRM encryption settings.
type DRMEncryptionConfig struct {
	KeyID               string `json:"keyId"`
	Key                 string `json:"key"`
	IV                  string `json:"iv,omitempty"`
	WidevinePSSH        string `json:"widevinePssh,omitempty"`
	PlayReadyPSSH       string `json:"playreadyPssh,omitempty"`
	FairPlayCertificate string `json:"fairplayCertificate,omitempty"`
	KeyURI              string `json:"keyUri,omitempty"`
}

// DefaultLLHLSConfig returns the default LL-HLS configuration.
func DefaultLLHLSConfig() *LLHLSConfig {
	return &LLHLSConfig{
		PartDuration:         0.5,
		SegmentDuration:      4.0,
		PartHoldBack:         1.5,
		CanBlockReload:       true,
		CanSkipUntil:         12.0,
		UseCMAF:              true,
		FrameRate:            30.0,
		EnableIFramePlaylist: true,
		Presets:              config.DefaultPresets(),
	}
}

// LLHLSTranscoder handles LL-HLS transcoding.
type LLHLSTranscoder struct {
	config     *LLHLSConfig
	ffmpegPath string
	logger     *slog.Logger
}

// NewLLHLSTranscoder creates a new LL-HLS transcoder.
func NewLLHLSTranscoder(cfg *LLHLSConfig, logger *slog.Logger) (*LLHLSTranscoder, error) {
	if cfg == nil {
		cfg = DefaultLLHLSConfig()
	}

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}

	if cfg.PartDuration <= 0 || cfg.PartDuration > 1.0 {
		cfg.PartDuration = 0.5
	}
	if cfg.PartHoldBack < cfg.PartDuration*3 {
		cfg.PartHoldBack = cfg.PartDuration * 3
	}

	return &LLHLSTranscoder{
		config:     cfg,
		ffmpegPath: ffmpegPath,
		logger:     logger,
	}, nil
}

// TranscodeResult contains the results of a transcoding operation.
type TranscodeResult struct {
	MasterPlaylistPath string            `json:"masterPlaylistPath"`
	VariantPlaylists   []string          `json:"variantPlaylists"`
	IFramePlaylists    []string          `json:"iframePlaylists,omitempty"`
	InitSegments       []string          `json:"initSegments,omitempty"`
	Duration           time.Duration     `json:"duration"`
	OutputSize         int64             `json:"outputSize"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// Transcode performs LL-HLS transcoding on the input file.
func (t *LLHLSTranscoder) Transcode(ctx context.Context, inputPath, outputDir string) (*TranscodeResult, error) {
	ctx, span := tracer.Start(ctx, "llhls-transcode",
		trace.WithAttributes(
			attribute.String("input.path", inputPath),
			attribute.String("output.dir", outputDir),
			attribute.Bool("cmaf.enabled", t.config.UseCMAF),
			attribute.Int("presets.count", len(t.config.Presets)),
		))
	defer span.End()

	startTime := time.Now()

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	for _, preset := range t.config.Presets {
		presetDir := filepath.Join(outputDir, preset.Name)
		if err := os.MkdirAll(presetDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create preset directory %s: %w", preset.Name, err)
		}
	}

	metadata, err := t.probeInput(ctx, inputPath)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to probe input: %w", err)
	}

	span.SetAttributes(
		attribute.String("input.duration", metadata["duration"]),
		attribute.String("input.video_codec", metadata["video_codec"]),
		attribute.String("input.resolution", fmt.Sprintf("%sx%s", metadata["width"], metadata["height"])),
	)

	args := t.buildFFmpegArgs(inputPath, outputDir)

	t.logger.InfoContext(ctx, "Starting LL-HLS transcoding",
		"input", inputPath,
		"output", outputDir,
		"presets", len(t.config.Presets),
		"cmaf", t.config.UseCMAF,
	)

	cmd := exec.CommandContext(ctx, t.ffmpegPath, args...)

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		t.monitorProgress(ctx, stderrPipe, metadata)
	}()

	go func() {
		defer wg.Done()
		io.Copy(io.Discard, stdoutPipe)
	}()

	cmdErr := cmd.Wait()
	wg.Wait()

	if cmdErr != nil {
		span.RecordError(cmdErr)
		return nil, fmt.Errorf("FFmpeg transcoding failed: %w", cmdErr)
	}

	masterPath := filepath.Join(outputDir, "master.m3u8")
	if err := t.generateMasterPlaylist(ctx, outputDir, masterPath); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to generate master playlist: %w", err)
	}

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

func (t *LLHLSTranscoder) buildFFmpegArgs(inputPath, outputDir string) []string {
	args := []string{
		"-i", inputPath,
		"-y",
	}

	var filterComplex strings.Builder
	var mapArgs []string

	for i, preset := range t.config.Presets {
		filterComplex.WriteString(fmt.Sprintf(
			"[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,"+
				"pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1[v%d];",
			preset.Width, preset.Height,
			preset.Width, preset.Height, i))

		mapArgs = append(mapArgs, "-map", fmt.Sprintf("[v%d]", i))
		mapArgs = append(mapArgs, "-map", "0:a?")
	}

	if filterComplex.Len() > 0 {
		args = append(args, "-filter_complex", strings.TrimSuffix(filterComplex.String(), ";"))
	}

	args = append(args, mapArgs...)

	for i, preset := range t.config.Presets {
		gopFrames := preset.GOPFrames()
		if gopFrames == 0 {
			gopFrames = int(t.config.FrameRate * 2)
		}

		args = append(args,
			fmt.Sprintf("-c:v:%d", i), "libx264",
			fmt.Sprintf("-b:v:%d", i), preset.VideoBitrate,
			fmt.Sprintf("-maxrate:v:%d", i), preset.MaxRate,
			fmt.Sprintf("-bufsize:v:%d", i), preset.BufSize,
			fmt.Sprintf("-profile:v:%d", i), preset.Profile,
			fmt.Sprintf("-level:v:%d", i), preset.Level,
			fmt.Sprintf("-preset:%d", i), "fast",
			fmt.Sprintf("-g:%d", i), strconv.Itoa(gopFrames),
			fmt.Sprintf("-keyint_min:%d", i), strconv.Itoa(gopFrames),
			fmt.Sprintf("-sc_threshold:%d", i), "0",
			fmt.Sprintf("-flags:%d", i), "+cgop",
		)

		args = append(args,
			fmt.Sprintf("-c:a:%d", i), "aac",
			fmt.Sprintf("-b:a:%d", i), preset.AudioBitrate,
			fmt.Sprintf("-ac:%d", i), "2",
			fmt.Sprintf("-ar:%d", i), "48000",
		)
	}

	args = append(args,
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%.1f", t.config.SegmentDuration),
		"-hls_list_size", "0",
		"-hls_playlist_type", "vod",
	)

	if t.config.UseCMAF {
		args = append(args,
			"-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", "init.mp4",
		)
	}

	hlsFlags := []string{
		"independent_segments",
		"program_date_time",
	}
	args = append(args, "-hls_flags", strings.Join(hlsFlags, "+"))

	segExt := ".ts"
	if t.config.UseCMAF {
		segExt = ".m4s"
	}
	args = append(args,
		"-hls_segment_filename", filepath.Join(outputDir, "%v", "seg_%05d"+segExt),
	)

	var varStreamMap strings.Builder
	for i, preset := range t.config.Presets {
		if i > 0 {
			varStreamMap.WriteString(" ")
		}
		varStreamMap.WriteString(fmt.Sprintf("v:%d,a:%d,name:%s", i, i, preset.Name))
	}
	args = append(args, "-var_stream_map", varStreamMap.String())

	args = append(args,
		"-master_pl_name", "master.m3u8",
		filepath.Join(outputDir, "%v", "playlist.m3u8"),
	)

	return args
}

func (t *LLHLSTranscoder) generateMasterPlaylist(ctx context.Context, outputDir, masterPath string) error {
	_, span := tracer.Start(ctx, "generate-master-playlist")
	defer span.End()

	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:9\n")
	playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	playlist.WriteString("\n")

	playlist.WriteString("#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"English\",")
	playlist.WriteString("LANGUAGE=\"en\",DEFAULT=YES,AUTOSELECT=YES,CHANNELS=\"2\"\n")
	playlist.WriteString("\n")

	if t.config.EnableIFramePlaylist {
		for _, preset := range t.config.Presets {
			iframePath := filepath.Join(preset.Name, "iframe.m3u8")
			if _, err := os.Stat(filepath.Join(outputDir, iframePath)); err == nil {
				playlist.WriteString(fmt.Sprintf(
					"#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,"+
						"CODECS=\"avc1.%s\",URI=\"%s\"\n",
					config.ParseBitrate(preset.VideoBitrate),
					preset.Width, preset.Height,
					config.GetAVCCodecString(preset.Profile, preset.Level),
					iframePath,
				))
			}
		}
		playlist.WriteString("\n")
	}

	for _, preset := range t.config.Presets {
		bandwidth := preset.PeakBandwidth()
		avgBandwidth := int64(float64(preset.Bandwidth()) * 0.9)

		playlist.WriteString(fmt.Sprintf(
			"#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d,"+
				"RESOLUTION=%dx%d,FRAME-RATE=%.3f,"+
				"CODECS=\"%s\",AUDIO=\"audio\"\n",
			bandwidth, avgBandwidth,
			preset.Width, preset.Height,
			t.config.FrameRate,
			preset.CodecString(),
		))
		playlist.WriteString(fmt.Sprintf("%s/playlist.m3u8\n", preset.Name))
	}

	return os.WriteFile(masterPath, []byte(playlist.String()), 0644)
}

func (t *LLHLSTranscoder) probeInput(ctx context.Context, inputPath string) (map[string]string, error) {
	_, span := tracer.Start(ctx, "probe-input")
	defer span.End()

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

	metadata := make(map[string]string)

	durationRe := regexp.MustCompile(`"duration"\s*:\s*"([^"]+)"`)
	if matches := durationRe.FindSubmatch(output); len(matches) > 1 {
		metadata["duration"] = string(matches[1])
	}

	codecRe := regexp.MustCompile(`"codec_name"\s*:\s*"(h264|hevc|vp9|av1)"`)
	if matches := codecRe.FindSubmatch(output); len(matches) > 1 {
		metadata["video_codec"] = string(matches[1])
	}

	widthRe := regexp.MustCompile(`"width"\s*:\s*(\d+)`)
	if matches := widthRe.FindSubmatch(output); len(matches) > 1 {
		metadata["width"] = string(matches[1])
	}

	heightRe := regexp.MustCompile(`"height"\s*:\s*(\d+)`)
	if matches := heightRe.FindSubmatch(output); len(matches) > 1 {
		metadata["height"] = string(matches[1])
	}

	fpsRe := regexp.MustCompile(`"r_frame_rate"\s*:\s*"(\d+)/(\d+)"`)
	if matches := fpsRe.FindSubmatch(output); len(matches) > 2 {
		num, _ := strconv.ParseFloat(string(matches[1]), 64)
		den, _ := strconv.ParseFloat(string(matches[2]), 64)
		if den > 0 {
			metadata["frame_rate"] = fmt.Sprintf("%.3f", num/den)
		}
	}

	bitrateRe := regexp.MustCompile(`"bit_rate"\s*:\s*"(\d+)"`)
	if matches := bitrateRe.FindSubmatch(output); len(matches) > 1 {
		metadata["bitrate"] = string(matches[1])
	}

	return metadata, nil
}

func (t *LLHLSTranscoder) monitorProgress(ctx context.Context, r io.Reader, metadata map[string]string) {
	scanner := bufio.NewScanner(r)
	progressRe := regexp.MustCompile(`time=(\d+:\d+:\d+\.\d+)`)
	frameRe := regexp.MustCompile(`frame=\s*(\d+)`)
	speedRe := regexp.MustCompile(`speed=\s*([0-9.]+)x`)

	var lastLogTime time.Time
	logInterval := 5 * time.Second

	for scanner.Scan() {
		line := scanner.Text()

		now := time.Now()
		if now.Sub(lastLogTime) < logInterval {
			continue
		}

		var timeStr, frameStr, speedStr string

		if matches := progressRe.FindStringSubmatch(line); len(matches) > 1 {
			timeStr = matches[1]
		}
		if matches := frameRe.FindStringSubmatch(line); len(matches) > 1 {
			frameStr = matches[1]
		}
		if matches := speedRe.FindStringSubmatch(line); len(matches) > 1 {
			speedStr = matches[1]
		}

		if timeStr != "" || frameStr != "" {
			t.logger.DebugContext(ctx, "Transcoding progress",
				"time", timeStr,
				"frame", frameStr,
				"speed", speedStr,
			)
			lastLogTime = now
		}

		if strings.Contains(strings.ToLower(line), "error") {
			t.logger.WarnContext(ctx, "FFmpeg warning/error", "output", line)
		}
	}
}

func (t *LLHLSTranscoder) getVariantPlaylists(outputDir string) []string {
	var playlists []string
	for _, preset := range t.config.Presets {
		playlistPath := filepath.Join(outputDir, preset.Name, "playlist.m3u8")
		if _, err := os.Stat(playlistPath); err == nil {
			playlists = append(playlists, playlistPath)
		}
	}
	return playlists
}

func (t *LLHLSTranscoder) getIFramePlaylists(outputDir string) []string {
	var playlists []string
	for _, preset := range t.config.Presets {
		iframePath := filepath.Join(outputDir, preset.Name, "iframe.m3u8")
		if _, err := os.Stat(iframePath); err == nil {
			playlists = append(playlists, iframePath)
		}
	}
	return playlists
}

func (t *LLHLSTranscoder) getInitSegments(outputDir string) []string {
	var segments []string
	for _, preset := range t.config.Presets {
		initPath := filepath.Join(outputDir, preset.Name, "init.mp4")
		if _, err := os.Stat(initPath); err == nil {
			segments = append(segments, initPath)
		}
	}
	return segments
}

// GetPresets returns the configured presets.
func (t *LLHLSTranscoder) GetPresets() []config.PresetConfig {
	return t.config.Presets
}

// GetConfig returns the transcoder configuration.
func (t *LLHLSTranscoder) GetConfig() *LLHLSConfig {
	return t.config
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

