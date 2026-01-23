package transcoder

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/amillerrr/hls-pipeline/internal/config"
)

// FFmpegArgsBuilder provides a fluent interface for building FFmpeg arguments.
type FFmpegArgsBuilder struct {
	args          []string
	inputs        []string
	videoFilters  []string
	audioFilters  []string
	globalOptions []string
	outputOptions []string
	presets       []config.PresetConfig
	drmConfig     *DRMEncryptionConfig
}

// NewFFmpegArgsBuilder creates a new FFmpeg args builder.
func NewFFmpegArgsBuilder() *FFmpegArgsBuilder {
	return &FFmpegArgsBuilder{
		args:          make([]string, 0),
		inputs:        make([]string, 0),
		videoFilters:  make([]string, 0),
		audioFilters:  make([]string, 0),
		globalOptions: make([]string, 0),
		outputOptions: make([]string, 0),
		presets:       make([]config.PresetConfig, 0),
	}
}

// Input adds an input file.
func (b *FFmpegArgsBuilder) Input(path string) *FFmpegArgsBuilder {
	b.inputs = append(b.inputs, "-i", path)
	return b
}

// InputWithOptions adds an input file with options.
func (b *FFmpegArgsBuilder) InputWithOptions(path string, options ...string) *FFmpegArgsBuilder {
	b.inputs = append(b.inputs, options...)
	b.inputs = append(b.inputs, "-i", path)
	return b
}

// Overwrite enables overwriting output files.
func (b *FFmpegArgsBuilder) Overwrite() *FFmpegArgsBuilder {
	b.globalOptions = append(b.globalOptions, "-y")
	return b
}

// HideStats hides encoding stats.
func (b *FFmpegArgsBuilder) HideStats() *FFmpegArgsBuilder {
	b.globalOptions = append(b.globalOptions, "-hide_banner", "-stats")
	return b
}

// Progress enables progress reporting to a URL.
func (b *FFmpegArgsBuilder) Progress(url string) *FFmpegArgsBuilder {
	b.globalOptions = append(b.globalOptions, "-progress", url)
	return b
}

// VideoCodec sets the video codec.
func (b *FFmpegArgsBuilder) VideoCodec(codec string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-c:v", codec)
	return b
}

// AudioCodec sets the audio codec.
func (b *FFmpegArgsBuilder) AudioCodec(codec string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-c:a", codec)
	return b
}

// CopyVideo copies video without re-encoding.
func (b *FFmpegArgsBuilder) CopyVideo() *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-c:v", "copy")
	return b
}

// CopyAudio copies audio without re-encoding.
func (b *FFmpegArgsBuilder) CopyAudio() *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-c:a", "copy")
	return b
}

// VideoBitrate sets the video bitrate.
func (b *FFmpegArgsBuilder) VideoBitrate(bitrate string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-b:v", bitrate)
	return b
}

// AudioBitrate sets the audio bitrate.
func (b *FFmpegArgsBuilder) AudioBitrate(bitrate string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-b:a", bitrate)
	return b
}

// MaxRate sets the maximum bitrate.
func (b *FFmpegArgsBuilder) MaxRate(rate string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-maxrate", rate)
	return b
}

// BufSize sets the buffer size.
func (b *FFmpegArgsBuilder) BufSize(size string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-bufsize", size)
	return b
}

// Resolution sets the output resolution.
func (b *FFmpegArgsBuilder) Resolution(width, height int) *FFmpegArgsBuilder {
	b.videoFilters = append(b.videoFilters, fmt.Sprintf("scale=%d:%d", width, height))
	return b
}

// Profile sets the H.264 profile.
func (b *FFmpegArgsBuilder) Profile(profile string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-profile:v", profile)
	return b
}

// Level sets the H.264 level.
func (b *FFmpegArgsBuilder) Level(level string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-level:v", level)
	return b
}

// Preset sets the encoding preset (ultrafast, fast, medium, slow, etc.).
func (b *FFmpegArgsBuilder) Preset(preset string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-preset", preset)
	return b
}

// GOP sets the Group of Pictures size.
func (b *FFmpegArgsBuilder) GOP(frames int) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-g", strconv.Itoa(frames))
	return b
}

// KeyintMin sets the minimum keyframe interval.
func (b *FFmpegArgsBuilder) KeyintMin(frames int) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-keyint_min", strconv.Itoa(frames))
	return b
}

// SceneChange sets scene change detection threshold.
func (b *FFmpegArgsBuilder) SceneChange(threshold int) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-sc_threshold", strconv.Itoa(threshold))
	return b
}

// FrameRate sets the output frame rate.
func (b *FFmpegArgsBuilder) FrameRate(fps float64) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-r", fmt.Sprintf("%.2f", fps))
	return b
}

// PixelFormat sets the pixel format.
func (b *FFmpegArgsBuilder) PixelFormat(format string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-pix_fmt", format)
	return b
}

// AudioChannels sets the number of audio channels.
func (b *FFmpegArgsBuilder) AudioChannels(channels int) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-ac", strconv.Itoa(channels))
	return b
}

// AudioSampleRate sets the audio sample rate.
func (b *FFmpegArgsBuilder) AudioSampleRate(rate int) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-ar", strconv.Itoa(rate))
	return b
}

// HLSOutput configures HLS output.
func (b *FFmpegArgsBuilder) HLSOutput(outputDir string, segmentDuration float64) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions,
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%.1f", segmentDuration),
		"-hls_list_size", "0",
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(outputDir, "segment_%05d.ts"),
	)
	b.args = append(b.args, filepath.Join(outputDir, "playlist.m3u8"))
	return b
}

// LLHLSOutput configures Low-Latency HLS output.
func (b *FFmpegArgsBuilder) LLHLSOutput(outputDir string, segmentDuration, partDuration float64) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions,
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%.1f", segmentDuration),
		"-hls_list_size", "0",
		"-hls_playlist_type", "event",
		"-hls_flags", "independent_segments+program_date_time",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(outputDir, "seg_%05d.m4s"),
	)

	// LL-HLS specific options
	if partDuration > 0 {
		b.outputOptions = append(b.outputOptions,
			"-hls_flags", "low_latency",
		)
	}

	b.args = append(b.args, filepath.Join(outputDir, "playlist.m3u8"))
	return b
}

// CMAFOutput configures CMAF output (fragmented MP4).
func (b *FFmpegArgsBuilder) CMAFOutput(outputDir string, segmentDuration float64) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions,
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%.1f", segmentDuration),
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(outputDir, "seg_%05d.m4s"),
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments+single_file",
	)
	b.args = append(b.args, filepath.Join(outputDir, "playlist.m3u8"))
	return b
}

// DASHOutput configures DASH output.
func (b *FFmpegArgsBuilder) DASHOutput(outputDir string, segmentDuration float64) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions,
		"-f", "dash",
		"-seg_duration", fmt.Sprintf("%.1f", segmentDuration),
		"-init_seg_name", "init_$RepresentationID$.m4s",
		"-media_seg_name", "seg_$RepresentationID$_$Number%05d$.m4s",
		"-use_template", "1",
		"-use_timeline", "1",
		"-adaptation_sets", "id=0,streams=v id=1,streams=a",
	)
	b.args = append(b.args, filepath.Join(outputDir, "manifest.mpd"))
	return b
}

// AddPreset adds an encoding preset to the builder.
func (b *FFmpegArgsBuilder) AddPreset(preset config.PresetConfig) *FFmpegArgsBuilder {
	b.presets = append(b.presets, preset)
	return b
}

// WithDRM adds DRM encryption configuration.
func (b *FFmpegArgsBuilder) WithDRM(cfg *DRMEncryptionConfig) *FFmpegArgsBuilder {
	b.drmConfig = cfg
	return b
}

// HLSKeyInfo adds HLS encryption key info file.
func (b *FFmpegArgsBuilder) HLSKeyInfo(keyInfoPath string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, "-hls_key_info_file", keyInfoPath)
	return b
}

// CustomOption adds a custom option.
func (b *FFmpegArgsBuilder) CustomOption(key, value string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, key, value)
	return b
}

// CustomFlag adds a custom flag (no value).
func (b *FFmpegArgsBuilder) CustomFlag(flag string) *FFmpegArgsBuilder {
	b.outputOptions = append(b.outputOptions, flag)
	return b
}

// VideoFilter adds a video filter.
func (b *FFmpegArgsBuilder) VideoFilter(filter string) *FFmpegArgsBuilder {
	b.videoFilters = append(b.videoFilters, filter)
	return b
}

// AudioFilter adds an audio filter.
func (b *FFmpegArgsBuilder) AudioFilter(filter string) *FFmpegArgsBuilder {
	b.audioFilters = append(b.audioFilters, filter)
	return b
}

// Output sets the output file path.
func (b *FFmpegArgsBuilder) Output(path string) *FFmpegArgsBuilder {
	b.args = append(b.args, path)
	return b
}

// Build constructs the final FFmpeg arguments array.
func (b *FFmpegArgsBuilder) Build() []string {
	args := make([]string, 0, len(b.globalOptions)+len(b.inputs)+len(b.outputOptions)+len(b.args)+10)

	// Global options first
	args = append(args, b.globalOptions...)

	// Input files
	args = append(args, b.inputs...)

	// Video filters
	if len(b.videoFilters) > 0 {
		args = append(args, "-vf", strings.Join(b.videoFilters, ","))
	}

	// Audio filters
	if len(b.audioFilters) > 0 {
		args = append(args, "-af", strings.Join(b.audioFilters, ","))
	}

	// Output options
	args = append(args, b.outputOptions...)

	// Output file(s)
	args = append(args, b.args...)

	return args
}

// BuildMultiOutput builds arguments for multi-bitrate output with filter_complex.
func (b *FFmpegArgsBuilder) BuildMultiOutput(outputDir string, segmentDuration float64) []string {
	if len(b.presets) == 0 {
		return b.Build()
	}

	args := make([]string, 0, 100)

	// Global options
	args = append(args, b.globalOptions...)

	// Input
	args = append(args, b.inputs...)

	// Build filter_complex for multi-bitrate
	var filterComplex strings.Builder
	var streamMaps []string

	for i, preset := range b.presets {
		// Scale filter for each preset
		filterComplex.WriteString(fmt.Sprintf("[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2[v%d];",
			preset.Width, preset.Height, preset.Width, preset.Height, i))
		streamMaps = append(streamMaps, fmt.Sprintf("[v%d]", i))
	}

	// Remove trailing semicolon
	filterStr := strings.TrimSuffix(filterComplex.String(), ";")
	args = append(args, "-filter_complex", filterStr)

	// Map each stream and set encoding options
	for i, preset := range b.presets {
		presetDir := filepath.Join(outputDir, preset.Name)

		args = append(args,
			"-map", fmt.Sprintf("[v%d]", i),
			"-map", "0:a",
			"-c:v", "libx264",
			"-c:a", "aac",
			"-b:v:"+strconv.Itoa(i), preset.VideoBitrate,
			"-b:a:"+strconv.Itoa(i), preset.AudioBitrate,
			"-profile:v", preset.Profile,
			"-level:v", preset.Level,
			"-g", strconv.Itoa(int(b.presets[0].FrameRate*segmentDuration)),
			"-keyint_min", strconv.Itoa(int(b.presets[0].FrameRate*segmentDuration)),
			"-sc_threshold", "0",
			"-f", "hls",
			"-hls_time", fmt.Sprintf("%.1f", segmentDuration),
			"-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", "init.mp4",
			"-hls_segment_filename", filepath.Join(presetDir, "seg_%05d.m4s"),
			"-hls_playlist_type", "vod",
			filepath.Join(presetDir, "playlist.m3u8"),
		)
	}

	return args
}

// String returns the command as a string for debugging.
func (b *FFmpegArgsBuilder) String() string {
	return "ffmpeg " + strings.Join(b.Build(), " ")
}

// PresetArgsBuilder helps build preset-specific arguments.
type PresetArgsBuilder struct {
	preset config.PresetConfig
}

// NewPresetArgsBuilder creates a builder for a specific preset.
func NewPresetArgsBuilder(preset config.PresetConfig) *PresetArgsBuilder {
	return &PresetArgsBuilder{preset: preset}
}

// BuildVideoArgs returns video encoding arguments for the preset.
func (p *PresetArgsBuilder) BuildVideoArgs() []string {
	args := []string{
		"-c:v", "libx264",
		"-b:v", p.preset.VideoBitrate,
		"-maxrate", p.preset.VideoBitrate,
		"-bufsize", fmt.Sprintf("%dk", config.ParseBitrate(p.preset.VideoBitrate)*2/1000),
		"-profile:v", p.preset.Profile,
		"-level:v", p.preset.Level,
	}

	if p.preset.Width > 0 && p.preset.Height > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d", p.preset.Width, p.preset.Height))
	}

	return args
}

// BuildAudioArgs returns audio encoding arguments for the preset.
func (p *PresetArgsBuilder) BuildAudioArgs() []string {
	return []string{
		"-c:a", "aac",
		"-b:a", p.preset.AudioBitrate,
		"-ac", "2",
		"-ar", "48000",
	}
}

// Helper functions

// monitorFFmpegProgress parses FFmpeg stderr for progress information.
func monitorFFmpegProgress(ctx context.Context, r io.Reader, progressFn func(float64), logger *slog.Logger) {
	if progressFn == nil {
		return
	}

	scanner := bufio.NewScanner(r)
	progressRegex := regexp.MustCompile(`time=(\d+):(\d+):(\d+)\.(\d+)`)

	for scanner.Scan() {
		line := scanner.Text()

		if matches := progressRegex.FindStringSubmatch(line); matches != nil {
			hours, _ := strconv.Atoi(matches[1])
			minutes, _ := strconv.Atoi(matches[2])
			seconds, _ := strconv.Atoi(matches[3])

			totalSeconds := float64(hours*3600 + minutes*60 + seconds)
			progressFn(totalSeconds)
		}

		// Log errors and warnings
		if strings.Contains(line, "Error") || strings.Contains(line, "error") {
			logger.WarnContext(ctx, "FFmpeg error", "line", line)
		}
	}
}

// parseFFprobeOutput parses ffprobe JSON output into a metadata map.
func parseFFprobeOutput(output []byte) (map[string]string, error) {
	var result struct {
		Format struct {
			Duration string `json:"duration"`
			BitRate  string `json:"bit_rate"`
			Size     string `json:"size"`
		} `json:"format"`
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			FrameRate  string `json:"r_frame_rate"`
			BitRate    string `json:"bit_rate"`
			Channels   int    `json:"channels"`
			SampleRate string `json:"sample_rate"`
		} `json:"streams"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	metadata := map[string]string{
		"duration": result.Format.Duration,
		"bit_rate": result.Format.BitRate,
		"size":     result.Format.Size,
	}

	for _, stream := range result.Streams {
		if stream.CodecType == "video" {
			metadata["video_codec"] = stream.CodecName
			metadata["width"] = strconv.Itoa(stream.Width)
			metadata["height"] = strconv.Itoa(stream.Height)
			metadata["frame_rate"] = stream.FrameRate
		} else if stream.CodecType == "audio" {
			metadata["audio_codec"] = stream.CodecName
			metadata["channels"] = strconv.Itoa(stream.Channels)
			metadata["sample_rate"] = stream.SampleRate
		}
	}

	return metadata, nil
}
