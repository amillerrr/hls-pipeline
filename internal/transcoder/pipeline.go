package transcoder

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/amillerrr/hls-pipeline/internal/config"
	"go.opentelemetry.io/otel/trace"
)

// Pipeline handles the FFmpeg -> Shaka Packager pipeline.
type Pipeline struct {
	config     *config.TranscodingConfig
	logger     *slog.Logger
	ffmpegPath string
	shakaPath  string
}

func NewPipeline(cfg *config.TranscodingConfig, logger *slog.Logger) (*Pipeline, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}
	shaka, err := exec.LookPath("packager")
	if err != nil {
		return nil, fmt.Errorf("shaka packager not found: %w", err)
	}

	return &Pipeline{
		config:     cfg,
		logger:     logger,
		ffmpegPath: ffmpeg,
		shakaPath:  shaka,
	}, nil
}

type JobInput struct {
	InputPath  string
	OutputDir  string
	StreamID   string
	Presets    []config.PresetConfig
}

func (p *Pipeline) Process(ctx context.Context, input JobInput) error {
	ctx, span := tracer.Start(ctx, "pipeline-process")
	defer span.End()

	// 1. Create a temporary directory for named pipes
	pipeDir, err := os.MkdirTemp("", "transcode-pipes-")
	if err != nil {
		return fmt.Errorf("failed to create pipe dir: %w", err)
	}
	defer os.RemoveAll(pipeDir)

	// 2. Setup Pipes and Commands
	var ffmpegArgs []string
	var shakaArgs []string

	// Basic FFmpeg Input
	ffmpegArgs = append(ffmpegArgs, "-y", "-i", input.InputPath)

	// Filter Complex for Scaling
	var filterComplex []string
	var mapArgs []string

	for i, preset := range input.Presets {
		// Create named pipe for this rendition
		pipeName := filepath.Join(pipeDir, fmt.Sprintf("stream_%d.ts", i))
		if err := syscall.Mkfifo(pipeName, 0600); err != nil {
			return fmt.Errorf("failed to create pipe %s: %w", pipeName, err)
		}

		// FFmpeg: Scale and Encode
		filterComplex = append(filterComplex, 
			fmt.Sprintf("[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2[v%d]", 
			preset.Width, preset.Height, preset.Width, preset.Height, i))
		
		mapArgs = append(mapArgs, 
			"-map", fmt.Sprintf("[v%d]", i), 
			"-c:v:"+fmt.Sprint(i), "libx264",
			"-b:v:"+fmt.Sprint(i), preset.VideoBitrate,
			"-profile:v:"+fmt.Sprint(i), preset.Profile,
			"-g", fmt.Sprint(int(preset.FrameRate * 2)), // 2-second GOP
			"-sc_threshold", "0",
			"-f", "mpegts", // Output to pipe as MPEG-TS
			pipeName,
		)

		// Shaka: Input config
		shakaStream := fmt.Sprintf(
			"in=%s,stream=video,output=%s/%s/video.m4s,init_segment=%s/%s/init.mp4,playlist_name=%s/%s/prog.m3u8,iframe_playlist_name=%s/%s/iframe.m3u8",
			pipeName, 
			input.OutputDir, preset.Name,
			input.OutputDir, preset.Name,
			input.OutputDir, preset.Name,
			input.OutputDir, preset.Name,
		)
		shakaArgs = append(shakaArgs, shakaStream)
	}

	// Audio (Process once)
	audioPipe := filepath.Join(pipeDir, "audio.ts")
	if err := syscall.Mkfifo(audioPipe, 0600); err != nil {
		return err
	}
	
	mapArgs = append(mapArgs, 
		"-map", "0:a", 
		"-c:a", "aac", 
		"-b:a", "128k", 
		"-f", "mpegts", 
		audioPipe,
	)

	shakaArgs = append(shakaArgs, fmt.Sprintf(
		"in=%s,stream=audio,output=%s/audio/audio.m4s,init_segment=%s/audio/init.mp4,playlist_name=%s/audio/prog.m3u8,hls_group_id=audio,hls_name=ENGLISH",
		audioPipe, input.OutputDir, input.OutputDir, input.OutputDir,
	))

	// Combine FFmpeg Args
	ffmpegArgs = append(ffmpegArgs, "-filter_complex", strings.Join(filterComplex, ";"))
	ffmpegArgs = append(ffmpegArgs, mapArgs...)

	// Global Shaka Args (CMAF / LL-HLS)
	shakaArgs = append(shakaArgs,
		"--enable_raw_key_encryption", // Allow raw keys if DRM is passed
		"--segment_duration", fmt.Sprint(p.config.SegmentDuration),
		"--hls_master_playlist_output", filepath.Join(input.OutputDir, "master.m3u8"),
		"--mpd_output", filepath.Join(input.OutputDir, "manifest.mpd"),
		"--hls_playlist_type", "VOD", // Or EVENT/LIVE based on context
	)

	if p.config.EnableLLHLS {
		shakaArgs = append(shakaArgs, "--low_latency_dash_mode")
	}

	// 3. Execution
	p.logger.InfoContext(ctx, "Starting transcoding pipeline", "presets", len(input.Presets))

	ffmpegCmd := exec.CommandContext(ctx, p.ffmpegPath, ffmpegArgs...)
	shakaCmd := exec.CommandContext(ctx, p.shakaPath, shakaArgs...)

	// Wire up stderr for logging
	ffmpegCmd.Stderr = os.Stderr
	shakaCmd.Stderr = os.Stderr

	if err := shakaCmd.Start(); err != nil {
		return fmt.Errorf("failed to start shaka: %w", err)
	}
	if err := ffmpegCmd.Start(); err != nil {
		// If ffmpeg fails, kill shaka
		_ = shakaCmd.Process.Kill()
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Wait for completion
	fErr := ffmpegCmd.Wait()
	sErr := shakaCmd.Wait()

	if fErr != nil {
		return fmt.Errorf("ffmpeg failed: %w", fErr)
	}
	if sErr != nil {
		return fmt.Errorf("shaka failed: %w", sErr)
	}

	return nil
}
