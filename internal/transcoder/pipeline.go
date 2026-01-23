package transcoder

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/amillerrr/hls-pipeline/internal/config"
	"github.com/amillerrr/hls-pipeline/internal/drm"
	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("hls-pipeline/transcoder")

// Pipeline handles the FFmpeg -> Shaka Packager pipeline.
type Pipeline struct {
	config      *config.TranscodingConfig
	logger      *slog.Logger
	ffmpegPath  string
	shakaPath   string
	keyProvider drm.KeyProvider
}

// NewPipeline creates a new transcoding pipeline instance.
func NewPipeline(cfg *config.TranscodingConfig, logger *slog.Logger, keyProvider drm.KeyProvider) (*Pipeline, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}
	
	// Check for 'packager' (common name) or 'shaka-packager'
	shaka, err := exec.LookPath("packager")
	if err != nil {
		shaka, err = exec.LookPath("shaka-packager")
		if err != nil {
			return nil, fmt.Errorf("shaka packager not found: %w", err)
		}
	}

	return &Pipeline{
		config:      cfg,
		logger:      logger,
		ffmpegPath:  ffmpeg,
		shakaPath:   shaka,
		keyProvider: keyProvider,
	}, nil
}

// Process executes the transcoding pipeline
func (p *Pipeline) Process(ctx context.Context, input JobInput) error {
	ctx, span := tracer.Start(ctx, "pipeline-process")
	defer span.End()

	// 1. Create a temporary directory for named pipes
	pipeDir, err := os.MkdirTemp("", "transcode-pipes-")
	if err != nil {
		return fmt.Errorf("failed to create pipe dir: %w", err)
	}
	defer os.RemoveAll(pipeDir) // Clean up pipes after job

	// 2. Setup Pipes and Commands
	var ffmpegArgs []string
	var shakaArgs []string

	// Basic FFmpeg Input
	ffmpegArgs = append(ffmpegArgs, "-y", "-i", input.InputPath)

	// Filter Complex for Scaling
	var filterComplex []string
	var mapArgs []string

	// Loop through presets to create the ABR ladder
	for i, preset := range input.Presets {
		// Create named pipe for this specific rendition
		pipeName := filepath.Join(pipeDir, fmt.Sprintf("stream_%d.ts", i))
		if err := syscall.Mkfifo(pipeName, 0600); err != nil {
			return fmt.Errorf("failed to create pipe %s: %w", pipeName, err)
		}

		// FFmpeg: Build the scaler filter
		filterComplex = append(filterComplex, 
			fmt.Sprintf("[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2[v%d]", 
			preset.Width, preset.Height, preset.Width, preset.Height, i))
		
		// FFmpeg: Map the scaler output to encoding parameters
		mapArgs = append(mapArgs, 
			"-map", fmt.Sprintf("[v%d]", i), 
			"-c:v:"+fmt.Sprint(i), "libx264",
			"-b:v:"+fmt.Sprint(i), preset.VideoBitrate,
			"-profile:v:"+fmt.Sprint(i), preset.Profile,
			"-g", fmt.Sprint(int(preset.FrameRate * 2)), // 2-second GOP size
			"-sc_threshold", "0",                        // Disable scene cut detection
			"-f", "mpegts",                              // Output format for the pipe
			pipeName,
		)

		// Shaka Packager: Configure the input stream from the pipe
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

	// Audio Handling
	audioPipe := filepath.Join(pipeDir, "audio.ts")
	if err := syscall.Mkfifo(audioPipe, 0600); err != nil {
		return fmt.Errorf("failed to create audio pipe: %w", err)
	}
	
	mapArgs = append(mapArgs, 
		"-map", "0:a", 
		"-c:a", "aac", 
		"-b:a", "128k", 
		"-ac", "2",
		"-f", "mpegts", 
		audioPipe,
	)

	shakaArgs = append(shakaArgs, fmt.Sprintf(
		"in=%s,stream=audio,output=%s/audio/audio.m4s,init_segment=%s/audio/init.mp4,playlist_name=%s/audio/prog.m3u8,hls_group_id=audio,hls_name=ENGLISH",
		audioPipe, input.OutputDir, input.OutputDir, input.OutputDir,
	))

	// Assemble final FFmpeg arguments
	ffmpegArgs = append(ffmpegArgs, "-filter_complex", strings.Join(filterComplex, ";"))
	ffmpegArgs = append(ffmpegArgs, mapArgs...)

	// Global Shaka Args
	shakaArgs = append(shakaArgs,
		"--enable_raw_key_encryption",
		"--segment_duration", fmt.Sprint(p.config.SegmentDuration),
		"--hls_master_playlist_output", filepath.Join(input.OutputDir, "master.m3u8"),
		"--mpd_output", filepath.Join(input.OutputDir, "manifest.mpd"),
		"--hls_playlist_type", "VOD", 
	)

	if p.config.EnableLLHLS {
		shakaArgs = append(shakaArgs, "--low_latency_dash_mode")
	}

	// DRM Integration
	if input.EnableDRM && p.keyProvider != nil {
		p.logger.InfoContext(ctx, "Enabling DRM encryption", "videoId", input.StreamID)
		
		key, err := p.keyProvider.GetContentKey(ctx, input.StreamID)
		if err != nil {
			return fmt.Errorf("failed to get content key: %w", err)
		}

		// Add keys
		shakaArgs = append(shakaArgs, 
			"--keys", fmt.Sprintf("key_id=%s:key=%s", key.KeyID, key.Key),
			"--protection_scheme", "cbcs", // Default to CBCS for Apple compatibility
		)

		// Get system configs (PSSH, License URLs)
		systems, err := p.keyProvider.GetDRMSystems(ctx, input.StreamID)
		if err != nil {
			p.logger.WarnContext(ctx, "Failed to get DRM system configs, continuing with basics", "error", err)
		} else {
			var protectionSystems []string
			for _, sys := range systems {
				if sys.System == drm.SystemWidevine {
					protectionSystems = append(protectionSystems, "Widevine")
					if sys.PSSH != "" {
						shakaArgs = append(shakaArgs, "--pssh", sys.PSSH)
					}
				} else if sys.System == drm.SystemPlayReady {
					protectionSystems = append(protectionSystems, "PlayReady")
				} else if sys.System == drm.SystemFairPlay {
					protectionSystems = append(protectionSystems, "FairPlay")
					if sys.LicenseURL != "" {
						shakaArgs = append(shakaArgs, "--hls_key_uri", sys.LicenseURL)
					}
				}
			}
			if len(protectionSystems) > 0 {
				shakaArgs = append(shakaArgs, "--protection_systems", strings.Join(protectionSystems, ","))
			}
		}
	}

	// 3. Execute Pipeline
	p.logger.InfoContext(ctx, "Starting transcoding pipeline", 
		"presets", len(input.Presets), 
		"output", input.OutputDir,
		"drm", input.EnableDRM,
	)

	ffmpegCmd := exec.CommandContext(ctx, p.ffmpegPath, ffmpegArgs...)
	shakaCmd := exec.CommandContext(ctx, p.shakaPath, shakaArgs...)

	ffmpegCmd.Stderr = os.Stderr
	shakaCmd.Stderr = os.Stderr

	if err := shakaCmd.Start(); err != nil {
		return fmt.Errorf("failed to start shaka: %w", err)
	}
	
	if err := ffmpegCmd.Start(); err != nil {
		_ = shakaCmd.Process.Kill()
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	fErr := ffmpegCmd.Wait()
	sErr := shakaCmd.Wait()

	if fErr != nil {
		return fmt.Errorf("ffmpeg execution failed: %w", fErr)
	}
	if sErr != nil {
		return fmt.Errorf("shaka execution failed: %w", sErr)
	}

	return nil
}
