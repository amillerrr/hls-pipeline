package transcoder

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/amillerrr/hls-pipeline/internal/metrics"
	"github.com/amillerrr/hls-pipeline/pkg/models"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

const (
	// HLSSegmentDuration is the duration of each HLS segment in seconds.
	HLSSegmentDuration = 6
)

var tracer = otel.Tracer("hls-transcoder")

// FFmpegConfig holds configuration for FFmpeg execution.
type FFmpegConfig struct {
	Presets []Preset
	Logger  *slog.Logger
}

// DefaultFFmpegConfig returns the default FFmpeg configuration.
func DefaultFFmpegConfig(logger *slog.Logger) *FFmpegConfig {
	return &FFmpegConfig{
		Presets: DefaultPresets,
		Logger:  logger,
	}
}

// Transcoder handles video transcoding operations.
type Transcoder struct {
	config *FFmpegConfig
}

// NewTranscoder creates a new Transcoder with the given configuration.
func NewTranscoder(config *FFmpegConfig) *Transcoder {
	return &Transcoder{config: config}
}

// TranscodeToHLS transcodes the input video to HLS format with multiple quality levels.
func (t *Transcoder) TranscodeToHLS(ctx context.Context, videoID, inputPath, hlsDir string) error {
	ctx, span := tracer.Start(ctx, "transcode-hls")
	defer span.End()

	start := time.Now()

	// Run FFmpeg transcoding
	if err := t.runFFmpeg(ctx, inputPath, hlsDir); err != nil {
		return err
	}

	// Generate master playlist
	if err := GenerateMasterPlaylist(hlsDir, t.config.Presets); err != nil {
		return fmt.Errorf("failed to generate master playlist: %w", err)
	}

	// Record metrics
	metrics.TranscodeDuration.Observe(time.Since(start).Seconds())

	return nil
}

// runFFmpeg executes the FFmpeg command for HLS transcoding.
func (t *Transcoder) runFFmpeg(ctx context.Context, inputPath, hlsDir string) error {
	ctx, span := tracer.Start(ctx, "ffmpeg-execute")
	defer span.End()

	args := t.buildFFmpegArgs(inputPath, hlsDir)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Monitor stderr for progress and errors
	go func() {
		defer wg.Done()
		t.monitorOutput(ctx, stderrPipe)
	}()

	// Drain stdout
	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.Discard, stdoutPipe)
	}()

	// Wait for command to complete
	cmdErr := cmd.Wait()
	wg.Wait()

	if cmdErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: context canceled", models.ErrFFmpegFailed)
		}
		return fmt.Errorf("%w: %v", models.ErrFFmpegFailed, cmdErr)
	}

	return nil
}

// buildFFmpegArgs constructs the FFmpeg command arguments.
func (t *Transcoder) buildFFmpegArgs(inputPath, hlsDir string) []string {
	presets := t.config.Presets

	args := []string{
		"-i", inputPath,
		"-preset", "veryfast",
		"-c:v", "libx264",
		"-profile:v", "main",
		"-level", "4.1",
		"-g", "100",
		"-keyint_min", "100",
		"-sc_threshold", "0",
		"-flags", "+cgop",
		"-filter_complex", BuildFilterComplex(presets),
	}

	// Add output streams for each quality preset
	for i, preset := range presets {
		streamArgs := []string{
			"-map", fmt.Sprintf("[v%dout]", i+1),
			"-map", "0:a?",
			fmt.Sprintf("-c:v:%d", i), "libx264",
			fmt.Sprintf("-b:v:%d", i), preset.Bitrate,
			fmt.Sprintf("-maxrate:v:%d", i), preset.MaxRate,
			fmt.Sprintf("-bufsize:v:%d", i), preset.BufSize,
			fmt.Sprintf("-c:a:%d", i), "aac",
			fmt.Sprintf("-b:a:%d", i), preset.AudioBPS,
			"-hls_time", fmt.Sprintf("%d", HLSSegmentDuration),
			"-hls_list_size", "0",
			"-hls_segment_filename", filepath.Join(hlsDir, preset.Name, "seg_%03d.ts"),
			filepath.Join(hlsDir, preset.Name, "playlist.m3u8"),
		}
		args = append(args, streamArgs...)
	}

	return args
}

// monitorOutput reads and logs FFmpeg output.
func (t *Transcoder) monitorOutput(ctx context.Context, r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			line := scanner.Text()
			if strings.Contains(line, "frame=") || strings.Contains(line, "time=") {
				t.config.Logger.Debug("FFmpeg progress", "output", line)
			} else if strings.Contains(line, "error") || strings.Contains(line, "Error") {
				t.config.Logger.Warn("FFmpeg warning", "output", line)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.config.Logger.Warn("FFmpeg output scanner error", "error", err)
	}
}

// GetPresets returns the configured presets.
func (t *Transcoder) GetPresets() []Preset {
	return t.config.Presets
}

// CalculateQualityMetrics calculates SSIM quality metrics for the transcoded video.
func (t *Transcoder) CalculateQualityMetrics(ctx context.Context, inputPath, hlsDir string) {
	ctx, span := tracer.Start(ctx, "calculate-quality")
	defer span.End()

	refFrame := filepath.Join(hlsDir, "ref_frame.png")
	distFrame := filepath.Join(hlsDir, "dist_frame.png")

	defer func() {
		// Clean up temporary frames
		_ = exec.CommandContext(ctx, "rm", "-f", refFrame, distFrame).Run()
	}()

	// Extract frame from source at 1 second
	err := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-ss", "00:00:01", "-i", inputPath,
		"-vf", "scale=1280:720", "-vframes", "1", refFrame,
	).Run()
	if err != nil {
		t.config.Logger.Warn("Failed to extract reference frame (video too short?)", "error", err)
		return
	}

	// Extract frame from 720p output
	playlist720 := filepath.Join(hlsDir, "720p", "playlist.m3u8")
	err = exec.CommandContext(ctx, "ffmpeg",
		"-y", "-ss", "00:00:01", "-i", playlist720,
		"-vframes", "1", distFrame,
	).Run()
	if err != nil {
		t.config.Logger.Warn("Failed to extract dist frame", "error", err)
		return
	}

	// Calculate SSIM
	ssimCmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", refFrame, "-i", distFrame,
		"-lavfi", "ssim", "-f", "null", "-")

	output, err := ssimCmd.CombinedOutput()
	if err != nil {
		t.config.Logger.Warn("Failed to calculate SSIM", "error", err)
		return
	}

	// Parse SSIM from output
	outputStr := string(output)
	if idx := strings.Index(outputStr, "All:"); idx != -1 {
		ssimStr := strings.TrimSpace(outputStr[idx+4 : min(idx+10, len(outputStr))])
		var ssim float64
		if _, err := fmt.Sscanf(ssimStr, "%f", &ssim); err == nil {
			metrics.RecordQuality("720p_vs_source", ssim)
			span.SetAttributes(attribute.Float64("ssim.720p", ssim))
			t.config.Logger.Info("SSIM score calculated", "value", ssim)
		}
	}
}
