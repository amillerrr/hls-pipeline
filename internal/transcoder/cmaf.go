package transcoder

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// CMAFConfig contains configuration for CMAF packaging.
type CMAFConfig struct {
	// SegmentDuration is the target segment duration in seconds.
	SegmentDuration float64 `json:"segmentDuration"`

	// FragmentDuration is the fragment duration within segments (for LL-DASH).
	FragmentDuration float64 `json:"fragmentDuration"`

	// EnableLowLatency enables low-latency CMAF features.
	EnableLowLatency bool `json:"enableLowLatency"`

	// GenerateHLS generates HLS playlists alongside DASH.
	GenerateHLS bool `json:"generateHls"`

	// GenerateDASH generates DASH manifests.
	GenerateDASH bool `json:"generateDash"`

	// UseShaka uses Shaka Packager instead of FFmpeg for packaging.
	UseShaka bool `json:"useShaka"`

	// DRMConfig contains DRM configuration if encryption is needed.
	DRMConfig *CMAFDRMConfig `json:"drmConfig,omitempty"`

	// Presets defines the encoding ladder.
	Presets []TranscodePreset `json:"presets"`
}

// CMAFDRMConfig contains DRM configuration for CMAF packaging.
type CMAFDRMConfig struct {
	// KeyID is the content key ID (hex string).
	KeyID string `json:"keyId"`

	// Key is the content encryption key (hex string).
	Key string `json:"key"`

	// PSSH boxes for each DRM system.
	WidevinePSSH  string `json:"widevinePssh,omitempty"`
	PlayReadyPSSH string `json:"playreadyPssh,omitempty"`

	// License URLs for each DRM system.
	WidevineLicenseURL  string `json:"widevineLicenseUrl,omitempty"`
	FairPlayLicenseURL  string `json:"fairplayLicenseUrl,omitempty"`
	PlayReadyLicenseURL string `json:"playreadyLicenseUrl,omitempty"`

	// FairPlay specific.
	FairPlayIV          string `json:"fairplayIv,omitempty"`
	FairPlayCertificate string `json:"fairplayCertificate,omitempty"`
}

// DefaultCMAFConfig returns the default CMAF configuration.
func DefaultCMAFConfig() *CMAFConfig {
	return &CMAFConfig{
		SegmentDuration:  4.0,
		FragmentDuration: 0.5,
		EnableLowLatency: true,
		GenerateHLS:      true,
		GenerateDASH:     true,
		UseShaka:         false, // Default to FFmpeg
		Presets:          DefaultPresets(),
	}
}

// CMAFPackager handles CMAF packaging.
type CMAFPackager struct {
	config      *CMAFConfig
	ffmpegPath  string
	shakaPath   string
	logger      *slog.Logger
	useFallback bool
}

// NewCMAFPackager creates a new CMAF packager.
func NewCMAFPackager(config *CMAFConfig, logger *slog.Logger) (*CMAFPackager, error) {
	if config == nil {
		config = DefaultCMAFConfig()
	}

	packager := &CMAFPackager{
		config: config,
		logger: logger,
	}

	// Find FFmpeg
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}
	packager.ffmpegPath = ffmpegPath

	// Try to find Shaka Packager (optional)
	shakaPath, err := exec.LookPath("packager")
	if err == nil {
		packager.shakaPath = shakaPath
	} else {
		// Also try shaka-packager
		shakaPath, err = exec.LookPath("shaka-packager")
		if err == nil {
			packager.shakaPath = shakaPath
		} else {
			logger.Warn("Shaka Packager not found, using FFmpeg fallback for CMAF")
			packager.useFallback = true
		}
	}

	return packager, nil
}

// CMAFResult contains the results of CMAF packaging.
type CMAFResult struct {
	// HLS outputs
	HLSMasterPlaylist string   `json:"hlsMasterPlaylist,omitempty"`
	HLSVariants       []string `json:"hlsVariants,omitempty"`

	// DASH outputs
	DASHManifest string `json:"dashManifest,omitempty"`

	// Segments and init files
	InitSegments []string `json:"initSegments"`
	Segments     []string `json:"segments"`

	// Timing
	Duration   time.Duration `json:"duration"`
	OutputSize int64         `json:"outputSize"`
}

// Package performs CMAF packaging on transcoded media.
func (p *CMAFPackager) Package(ctx context.Context, inputDir, outputDir string) (*CMAFResult, error) {
	ctx, span := tracer.Start(ctx, "cmaf-package",
		trace.WithAttributes(
			attribute.String("input.dir", inputDir),
			attribute.String("output.dir", outputDir),
			attribute.Bool("use_shaka", p.config.UseShaka && !p.useFallback),
			attribute.Bool("drm.enabled", p.config.DRMConfig != nil),
		))
	defer span.End()

	startTime := time.Now()

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	var result *CMAFResult
	var err error

	if p.config.UseShaka && !p.useFallback {
		result, err = p.packageWithShaka(ctx, inputDir, outputDir)
	} else {
		result, err = p.packageWithFFmpeg(ctx, inputDir, outputDir)
	}

	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	result.Duration = time.Since(startTime)

	// Calculate output size
	result.OutputSize, _ = calculateDirSize(outputDir)

	span.SetAttributes(
		attribute.Int64("output.size_bytes", result.OutputSize),
		attribute.Int64("package.duration_ms", result.Duration.Milliseconds()),
	)

	return result, nil
}

// packageWithFFmpeg uses FFmpeg for CMAF packaging.
func (p *CMAFPackager) packageWithFFmpeg(ctx context.Context, inputDir, outputDir string) (*CMAFResult, error) {
	result := &CMAFResult{
		InitSegments: []string{},
		Segments:     []string{},
	}

	// Find input files for each preset
	for _, preset := range p.config.Presets {
		presetDir := filepath.Join(outputDir, preset.Name)
		if err := os.MkdirAll(presetDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create preset directory: %w", err)
		}

		// Look for input file
		inputPattern := filepath.Join(inputDir, preset.Name, "*.mp4")
		matches, _ := filepath.Glob(inputPattern)
		
		if len(matches) == 0 {
			// Try looking for the variant playlist and associated segments
			inputPattern = filepath.Join(inputDir, preset.Name, "playlist.m3u8")
			if _, err := os.Stat(inputPattern); err != nil {
				p.logger.WarnContext(ctx, "No input found for preset", "preset", preset.Name)
				continue
			}
		}

		inputFile := matches[0]
		if len(matches) == 0 {
			inputFile = inputPattern
		}

		// Build FFmpeg command for CMAF output
		args := p.buildFFmpegCMAFArgs(inputFile, presetDir, preset)

		cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("FFmpeg CMAF packaging failed for %s: %w", preset.Name, err)
		}

		// Record output files
		initPath := filepath.Join(presetDir, "init.mp4")
		if _, err := os.Stat(initPath); err == nil {
			result.InitSegments = append(result.InitSegments, initPath)
		}

		// Record HLS variant
		hlsPath := filepath.Join(presetDir, "playlist.m3u8")
		if _, err := os.Stat(hlsPath); err == nil {
			result.HLSVariants = append(result.HLSVariants, hlsPath)
		}
	}

	// Generate master playlist
	if p.config.GenerateHLS {
		masterPath := filepath.Join(outputDir, "master.m3u8")
		if err := p.generateHLSMaster(masterPath); err != nil {
			return nil, fmt.Errorf("failed to generate HLS master playlist: %w", err)
		}
		result.HLSMasterPlaylist = masterPath
	}

	// Generate DASH manifest
	if p.config.GenerateDASH {
		dashPath := filepath.Join(outputDir, "manifest.mpd")
		if err := p.generateDASHManifest(ctx, outputDir, dashPath); err != nil {
			p.logger.WarnContext(ctx, "Failed to generate DASH manifest", "error", err)
		} else {
			result.DASHManifest = dashPath
		}
	}

	return result, nil
}

// buildFFmpegCMAFArgs builds FFmpeg arguments for CMAF output.
func (p *CMAFPackager) buildFFmpegCMAFArgs(inputFile, outputDir string, preset TranscodePreset) []string {
	args := []string{
		"-i", inputFile,
		"-y",
		"-c:v", "copy",
		"-c:a", "copy",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%.1f", p.config.SegmentDuration),
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(outputDir, "seg_%03d.m4s"),
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments+single_file",
	}

	// Add encryption if DRM is configured
	if p.config.DRMConfig != nil {
		args = append(args,
			"-hls_key_info_file", p.createKeyInfoFile(outputDir),
		)
	}

	args = append(args, filepath.Join(outputDir, "playlist.m3u8"))

	return args
}

// packageWithShaka uses Shaka Packager for CMAF packaging.
func (p *CMAFPackager) packageWithShaka(ctx context.Context, inputDir, outputDir string) (*CMAFResult, error) {
	result := &CMAFResult{
		InitSegments: []string{},
		Segments:     []string{},
	}

	var streamDescriptors []string

	// Build stream descriptors for each preset
	for i, preset := range p.config.Presets {
		inputPattern := filepath.Join(inputDir, preset.Name, "*.mp4")
		matches, _ := filepath.Glob(inputPattern)
		
		if len(matches) == 0 {
			continue
		}

		inputFile := matches[0]
		presetDir := filepath.Join(outputDir, preset.Name)
		
		if err := os.MkdirAll(presetDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create preset directory: %w", err)
		}

		// Video stream descriptor
		videoDesc := fmt.Sprintf(
			"in=%s,stream=video,init_segment=%s/init_video.mp4,segment_template=%s/seg_$Number$.m4s,playlist_name=%s/video.m3u8",
			inputFile,
			presetDir,
			presetDir,
			presetDir,
		)
		streamDescriptors = append(streamDescriptors, videoDesc)

		// Audio stream descriptor (only for first preset to avoid duplicate audio)
		if i == 0 {
			audioDesc := fmt.Sprintf(
				"in=%s,stream=audio,init_segment=%s/init_audio.mp4,segment_template=%s/audio_$Number$.m4s,playlist_name=%s/audio.m3u8,hls_group_id=audio,hls_name=English",
				inputFile,
				outputDir,
				outputDir,
				outputDir,
			)
			streamDescriptors = append(streamDescriptors, audioDesc)
		}

		result.InitSegments = append(result.InitSegments, filepath.Join(presetDir, "init_video.mp4"))
	}

	if len(streamDescriptors) == 0 {
		return nil, fmt.Errorf("no input files found for packaging")
	}

	// Build Shaka Packager command
	args := streamDescriptors

	// Add DASH output
	if p.config.GenerateDASH {
		dashPath := filepath.Join(outputDir, "manifest.mpd")
		args = append(args, "--mpd_output", dashPath)
		result.DASHManifest = dashPath
	}

	// Add HLS output
	if p.config.GenerateHLS {
		hlsPath := filepath.Join(outputDir, "master.m3u8")
		args = append(args, "--hls_master_playlist_output", hlsPath)
		result.HLSMasterPlaylist = hlsPath
	}

	// Segment duration
	args = append(args,
		"--segment_duration", fmt.Sprintf("%.1f", p.config.SegmentDuration),
		"--fragment_duration", fmt.Sprintf("%.1f", p.config.FragmentDuration),
	)

	// Low-latency options
	if p.config.EnableLowLatency {
		args = append(args,
			"--low_latency_dash_mode=true",
			"--utc_timings", "urn:mpeg:dash:utc:http-xsdate:2014=https://time.akamai.com/?iso",
		)
	}

	// DRM configuration
	if p.config.DRMConfig != nil {
		args = append(args,
			"--enable_raw_key_encryption",
			"--keys", fmt.Sprintf("key_id=%s:key=%s", p.config.DRMConfig.KeyID, p.config.DRMConfig.Key),
		)

		if p.config.DRMConfig.WidevinePSSH != "" {
			args = append(args, "--pssh", p.config.DRMConfig.WidevinePSSH)
		}
	}

	p.logger.InfoContext(ctx, "Running Shaka Packager", "args_count", len(args))

	cmd := exec.CommandContext(ctx, p.shakaPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("Shaka Packager failed: %w", err)
	}

	// Find HLS variants
	for _, preset := range p.config.Presets {
		hlsPath := filepath.Join(outputDir, preset.Name, "video.m3u8")
		if _, err := os.Stat(hlsPath); err == nil {
			result.HLSVariants = append(result.HLSVariants, hlsPath)
		}
	}

	return result, nil
}

// generateHLSMaster generates an HLS master playlist.
func (p *CMAFPackager) generateHLSMaster(masterPath string) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:7\n")
	playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n\n")

	for _, preset := range p.config.Presets {
		bandwidth := parseBitrate(preset.VideoBitrate) + parseBitrate(preset.AudioBitrate)

		playlist.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"avc1.%s,mp4a.40.2\"\n",
			bandwidth,
			preset.Width,
			preset.Height,
			getAVCCodecString(preset.Profile, preset.Level),
		))
		playlist.WriteString(fmt.Sprintf("%s/playlist.m3u8\n", preset.Name))
	}

	return os.WriteFile(masterPath, []byte(playlist.String()), 0644)
}

// generateDASHManifest generates a DASH manifest.
func (p *CMAFPackager) generateDASHManifest(ctx context.Context, outputDir, manifestPath string) error {
	// For FFmpeg-based packaging, we need to generate DASH separately
	// This is a simplified implementation - in production, use Shaka Packager
	
	var mpd strings.Builder

	mpd.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	mpd.WriteString("\n")
	mpd.WriteString(`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" `)
	mpd.WriteString(`xmlns:cenc="urn:mpeg:cenc:2013" `)
	mpd.WriteString(`type="static" `)
	mpd.WriteString(fmt.Sprintf(`minBufferTime="PT%.1fS" `, p.config.SegmentDuration))
	mpd.WriteString(`profiles="urn:mpeg:dash:profile:isoff-live:2011">`)
	mpd.WriteString("\n")

	mpd.WriteString(`  <Period id="0" start="PT0S">`)
	mpd.WriteString("\n")
	mpd.WriteString(`    <AdaptationSet mimeType="video/mp4" segmentAlignment="true" startWithSAP="1">`)
	mpd.WriteString("\n")

	for _, preset := range p.config.Presets {
		bandwidth := parseBitrate(preset.VideoBitrate)
		mpd.WriteString(fmt.Sprintf(`      <Representation id="%s" bandwidth="%d" width="%d" height="%d" codecs="avc1.%s">`,
			preset.Name, bandwidth, preset.Width, preset.Height, getAVCCodecString(preset.Profile, preset.Level)))
		mpd.WriteString("\n")
		mpd.WriteString(fmt.Sprintf(`        <BaseURL>%s/</BaseURL>`, preset.Name))
		mpd.WriteString("\n")
		mpd.WriteString(`        <SegmentTemplate initialization="init.mp4" media="seg_$Number$.m4s" startNumber="0"/>`)
		mpd.WriteString("\n")
		mpd.WriteString(`      </Representation>`)
		mpd.WriteString("\n")
	}

	mpd.WriteString(`    </AdaptationSet>`)
	mpd.WriteString("\n")
	mpd.WriteString(`  </Period>`)
	mpd.WriteString("\n")
	mpd.WriteString(`</MPD>`)
	mpd.WriteString("\n")

	return os.WriteFile(manifestPath, []byte(mpd.String()), 0644)
}

// createKeyInfoFile creates an HLS key info file for encryption.
func (p *CMAFPackager) createKeyInfoFile(outputDir string) string {
	if p.config.DRMConfig == nil {
		return ""
	}

	keyInfoPath := filepath.Join(outputDir, "key_info.txt")
	keyPath := filepath.Join(outputDir, "encryption.key")

	// Write the key file
	keyBytes := make([]byte, 16)
	// In production, decode the hex key properly
	os.WriteFile(keyPath, keyBytes, 0600)

	// Write key info file
	// Format: key URI\nkey path\nIV (optional)
	content := fmt.Sprintf("%s\n%s\n%s",
		p.config.DRMConfig.FairPlayLicenseURL,
		keyPath,
		p.config.DRMConfig.FairPlayIV,
	)
	os.WriteFile(keyInfoPath, []byte(content), 0644)

	return keyInfoPath
}

// EncryptCMAF adds encryption to existing CMAF content.
func (p *CMAFPackager) EncryptCMAF(ctx context.Context, inputDir, outputDir string, drmConfig *CMAFDRMConfig) error {
	ctx, span := tracer.Start(ctx, "cmaf-encrypt",
		trace.WithAttributes(
			attribute.String("input.dir", inputDir),
			attribute.String("output.dir", outputDir),
		))
	defer span.End()

	if p.shakaPath == "" {
		return fmt.Errorf("Shaka Packager required for encryption")
	}

	// Build re-packaging command with encryption
	var args []string

	for _, preset := range p.config.Presets {
		initPath := filepath.Join(inputDir, preset.Name, "init.mp4")
		if _, err := os.Stat(initPath); err != nil {
			continue
		}

		presetOutputDir := filepath.Join(outputDir, preset.Name)
		os.MkdirAll(presetOutputDir, 0755)

		segPattern := filepath.Join(inputDir, preset.Name, "seg_*.m4s")
		matches, _ := filepath.Glob(segPattern)

		for _, segPath := range matches {
			args = append(args, fmt.Sprintf(
				"in=%s,stream=video,output=%s",
				segPath,
				filepath.Join(presetOutputDir, filepath.Base(segPath)),
			))
		}
	}

	args = append(args,
		"--enable_raw_key_encryption",
		"--keys", fmt.Sprintf("key_id=%s:key=%s", drmConfig.KeyID, drmConfig.Key),
		"--protection_scheme", "cbcs", // Common encryption scheme for HLS
	)

	cmd := exec.CommandContext(ctx, p.shakaPath, args...)
	return cmd.Run()
}
