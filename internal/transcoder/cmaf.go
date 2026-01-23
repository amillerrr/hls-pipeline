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

	"github.com/amillerrr/hls-pipeline/internal/config"
)

// CMAFConfig contains configuration for CMAF packaging.
type CMAFConfig struct {
	SegmentDuration  float64              `json:"segmentDuration"`
	FragmentDuration float64              `json:"fragmentDuration"`
	EnableLowLatency bool                 `json:"enableLowLatency"`
	GenerateHLS      bool                 `json:"generateHls"`
	GenerateDASH     bool                 `json:"generateDash"`
	UseShaka         bool                 `json:"useShaka"`
	DRMConfig        *CMAFDRMConfig       `json:"drmConfig,omitempty"`
	Presets          []config.PresetConfig `json:"presets"`
	UTCTimingURL     string               `json:"utcTimingUrl,omitempty"`
}

// CMAFDRMConfig contains DRM configuration for CMAF packaging.
type CMAFDRMConfig struct {
	KeyID               string `json:"keyId"`
	Key                 string `json:"key"`
	WidevinePSSH        string `json:"widevinePssh,omitempty"`
	PlayReadyPSSH       string `json:"playreadyPssh,omitempty"`
	WidevineLicenseURL  string `json:"widevineLicenseUrl,omitempty"`
	FairPlayLicenseURL  string `json:"fairplayLicenseUrl,omitempty"`
	PlayReadyLicenseURL string `json:"playreadyLicenseUrl,omitempty"`
	FairPlayIV          string `json:"fairplayIv,omitempty"`
	FairPlayCertificate string `json:"fairplayCertificate,omitempty"`
	EncryptionScheme    string `json:"encryptionScheme,omitempty"`
}

// DefaultCMAFConfig returns the default CMAF configuration.
func DefaultCMAFConfig() *CMAFConfig {
	return &CMAFConfig{
		SegmentDuration:  4.0,
		FragmentDuration: 0.5,
		EnableLowLatency: true,
		GenerateHLS:      true,
		GenerateDASH:     true,
		UseShaka:         false,
		Presets:          config.DefaultPresets(),
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
func NewCMAFPackager(cfg *CMAFConfig, logger *slog.Logger) (*CMAFPackager, error) {
	if cfg == nil {
		cfg = DefaultCMAFConfig()
	}

	packager := &CMAFPackager{
		config: cfg,
		logger: logger,
	}

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}
	packager.ffmpegPath = ffmpegPath

	if cfg.UseShaka {
		shakaPath, err := exec.LookPath("packager")
		if err != nil {
			shakaPath, err = exec.LookPath("shaka-packager")
		}
		if err == nil {
			packager.shakaPath = shakaPath
			logger.Info("Shaka Packager found", "path", shakaPath)
		} else {
			logger.Warn("Shaka Packager not found, using FFmpeg fallback")
			packager.useFallback = true
		}
	}

	return packager, nil
}

// CMAFResult contains the results of CMAF packaging.
type CMAFResult struct {
	HLSMasterPlaylist string   `json:"hlsMasterPlaylist,omitempty"`
	HLSVariants       []string `json:"hlsVariants,omitempty"`
	DASHManifest      string   `json:"dashManifest,omitempty"`
	InitSegments      []string `json:"initSegments"`
	Segments          []string `json:"segments"`
	Encrypted         bool     `json:"encrypted"`
	KeyID             string   `json:"keyId,omitempty"`
	Duration          time.Duration `json:"duration"`
	OutputSize        int64    `json:"outputSize"`
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
	result.OutputSize, _ = calculateDirSize(outputDir)

	if p.config.DRMConfig != nil {
		result.Encrypted = true
		result.KeyID = p.config.DRMConfig.KeyID
	}

	span.SetAttributes(
		attribute.Int64("output.size_bytes", result.OutputSize),
		attribute.Int64("package.duration_ms", result.Duration.Milliseconds()),
	)

	return result, nil
}

func (p *CMAFPackager) packageWithShaka(ctx context.Context, inputDir, outputDir string) (*CMAFResult, error) {
	_, span := tracer.Start(ctx, "shaka-package")
	defer span.End()

	result := &CMAFResult{
		InitSegments: []string{},
		Segments:     []string{},
	}

	var streamDescriptors []string

	for i, preset := range p.config.Presets {
		inputFile := p.findInputFile(inputDir, preset.Name)
		if inputFile == "" {
			p.logger.WarnContext(ctx, "No input found for preset", "preset", preset.Name)
			continue
		}

		presetDir := filepath.Join(outputDir, preset.Name)
		if err := os.MkdirAll(presetDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create preset directory: %w", err)
		}

		videoDesc := fmt.Sprintf(
			"in=%s,stream=video,"+
				"init_segment=%s/init_video.mp4,"+
				"segment_template=%s/seg_$Number%%05d$.m4s,"+
				"playlist_name=%s/video.m3u8",
			inputFile, presetDir, presetDir, presetDir,
		)
		streamDescriptors = append(streamDescriptors, videoDesc)

		if i == 0 {
			audioDesc := fmt.Sprintf(
				"in=%s,stream=audio,"+
					"init_segment=%s/init_audio.mp4,"+
					"segment_template=%s/audio_$Number%%05d$.m4s,"+
					"playlist_name=%s/audio.m3u8,"+
					"hls_group_id=audio,hls_name=English",
				inputFile, outputDir, outputDir, outputDir,
			)
			streamDescriptors = append(streamDescriptors, audioDesc)
		}

		result.InitSegments = append(result.InitSegments, filepath.Join(presetDir, "init_video.mp4"))
	}

	if len(streamDescriptors) == 0 {
		return nil, fmt.Errorf("no input files found for packaging")
	}

	args := streamDescriptors

	if p.config.GenerateDASH {
		dashPath := filepath.Join(outputDir, "manifest.mpd")
		args = append(args, "--mpd_output", dashPath)
		result.DASHManifest = dashPath
	}

	if p.config.GenerateHLS {
		hlsPath := filepath.Join(outputDir, "master.m3u8")
		args = append(args, "--hls_master_playlist_output", hlsPath)
		result.HLSMasterPlaylist = hlsPath
	}

	args = append(args,
		"--segment_duration", fmt.Sprintf("%.1f", p.config.SegmentDuration),
		"--fragment_duration", fmt.Sprintf("%.1f", p.config.FragmentDuration),
	)

	if p.config.EnableLowLatency {
		args = append(args, "--low_latency_dash_mode=true")
		if p.config.UTCTimingURL != "" {
			args = append(args, "--utc_timings",
				fmt.Sprintf("urn:mpeg:dash:utc:http-xsdate:2014=%s", p.config.UTCTimingURL))
		}
	}

	if p.config.DRMConfig != nil {
		args = p.addDRMArgs(args)
	}

	p.logger.InfoContext(ctx, "Running Shaka Packager",
		"args_count", len(args),
		"drm_enabled", p.config.DRMConfig != nil,
	)

	cmd := exec.CommandContext(ctx, p.shakaPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		p.logger.ErrorContext(ctx, "Shaka Packager failed",
			"error", err,
			"output", string(output),
		)
		return nil, fmt.Errorf("Shaka Packager failed: %w", err)
	}

	for _, preset := range p.config.Presets {
		hlsPath := filepath.Join(outputDir, preset.Name, "video.m3u8")
		if _, err := os.Stat(hlsPath); err == nil {
			result.HLSVariants = append(result.HLSVariants, hlsPath)
		}
	}

	return result, nil
}

func (p *CMAFPackager) addDRMArgs(args []string) []string {
	drm := p.config.DRMConfig

	args = append(args,
		"--enable_raw_key_encryption",
		"--keys", fmt.Sprintf("key_id=%s:key=%s", drm.KeyID, drm.Key),
	)

	scheme := drm.EncryptionScheme
	if scheme == "" {
		scheme = "cbcs"
	}
	args = append(args, "--protection_scheme", scheme)

	var systems []string
	if drm.WidevinePSSH != "" || drm.WidevineLicenseURL != "" {
		systems = append(systems, "Widevine")
	}
	if drm.PlayReadyPSSH != "" || drm.PlayReadyLicenseURL != "" {
		systems = append(systems, "PlayReady")
	}
	if drm.FairPlayLicenseURL != "" {
		systems = append(systems, "FairPlay")
	}
	if len(systems) > 0 {
		args = append(args, "--protection_systems", strings.Join(systems, ","))
	}

	if drm.WidevinePSSH != "" {
		args = append(args, "--pssh", drm.WidevinePSSH)
	}
	if drm.PlayReadyPSSH != "" {
		args = append(args, "--pssh", drm.PlayReadyPSSH)
	}

	if drm.FairPlayLicenseURL != "" {
		args = append(args, "--hls_key_uri", drm.FairPlayLicenseURL)
		if drm.FairPlayIV != "" {
			args = append(args, "--hls_iv", drm.FairPlayIV)
		}
	}

	return args
}

func (p *CMAFPackager) packageWithFFmpeg(ctx context.Context, inputDir, outputDir string) (*CMAFResult, error) {
	_, span := tracer.Start(ctx, "ffmpeg-cmaf-package")
	defer span.End()

	result := &CMAFResult{
		InitSegments: []string{},
		Segments:     []string{},
	}

	for _, preset := range p.config.Presets {
		presetDir := filepath.Join(outputDir, preset.Name)
		if err := os.MkdirAll(presetDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create preset directory: %w", err)
		}

		inputFile := p.findInputFile(inputDir, preset.Name)
		if inputFile == "" {
			inputFile = filepath.Join(inputDir, preset.Name, "playlist.m3u8")
			if _, err := os.Stat(inputFile); err != nil {
				p.logger.WarnContext(ctx, "No input found for preset", "preset", preset.Name)
				continue
			}
		}

		args := p.buildFFmpegCMAFArgs(inputFile, presetDir, preset)

		cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			p.logger.ErrorContext(ctx, "FFmpeg CMAF packaging failed",
				"preset", preset.Name,
				"error", err,
				"output", string(output),
			)
			return nil, fmt.Errorf("FFmpeg CMAF packaging failed for %s: %w", preset.Name, err)
		}

		initPath := filepath.Join(presetDir, "init.mp4")
		if _, err := os.Stat(initPath); err == nil {
			result.InitSegments = append(result.InitSegments, initPath)
		}

		hlsPath := filepath.Join(presetDir, "playlist.m3u8")
		if _, err := os.Stat(hlsPath); err == nil {
			result.HLSVariants = append(result.HLSVariants, hlsPath)
		}
	}

	if p.config.GenerateHLS {
		masterPath := filepath.Join(outputDir, "master.m3u8")
		if err := p.generateHLSMaster(masterPath); err != nil {
			return nil, fmt.Errorf("failed to generate HLS master playlist: %w", err)
		}
		result.HLSMasterPlaylist = masterPath
	}

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

func (p *CMAFPackager) buildFFmpegCMAFArgs(inputFile, outputDir string, preset config.PresetConfig) []string {
	args := []string{
		"-i", inputFile,
		"-y",
		"-c:v", "copy",
		"-c:a", "copy",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%.1f", p.config.SegmentDuration),
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(outputDir, "seg_%05d.m4s"),
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments+single_file",
	}

	if p.config.DRMConfig != nil && p.config.DRMConfig.FairPlayLicenseURL != "" {
		keyInfoFile := p.createKeyInfoFile(outputDir)
		if keyInfoFile != "" {
			args = append(args, "-hls_key_info_file", keyInfoFile)
		}
	}

	args = append(args, filepath.Join(outputDir, "playlist.m3u8"))

	return args
}

func (p *CMAFPackager) findInputFile(inputDir, presetName string) string {
	patterns := []string{
		filepath.Join(inputDir, presetName, "*.mp4"),
		filepath.Join(inputDir, presetName+".mp4"),
		filepath.Join(inputDir, presetName, "output.mp4"),
	}

	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			return matches[0]
		}
	}

	return ""
}

func (p *CMAFPackager) generateHLSMaster(masterPath string) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:7\n")
	playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n\n")

	playlist.WriteString("#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"English\",")
	playlist.WriteString("LANGUAGE=\"en\",DEFAULT=YES,AUTOSELECT=YES,CHANNELS=\"2\"\n\n")

	for _, preset := range p.config.Presets {
		bandwidth := preset.PeakBandwidth()

		playlist.WriteString(fmt.Sprintf(
			"#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d,"+
				"RESOLUTION=%dx%d,CODECS=\"%s\",AUDIO=\"audio\"\n",
			bandwidth,
			int64(float64(preset.Bandwidth())*0.9),
			preset.Width, preset.Height,
			preset.CodecString(),
		))
		playlist.WriteString(fmt.Sprintf("%s/playlist.m3u8\n", preset.Name))
	}

	return os.WriteFile(masterPath, []byte(playlist.String()), 0644)
}

func (p *CMAFPackager) generateDASHManifest(ctx context.Context, outputDir, manifestPath string) error {
	var mpd strings.Builder

	mpd.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	mpd.WriteString(`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" `)
	mpd.WriteString(`xmlns:cenc="urn:mpeg:cenc:2013" `)
	mpd.WriteString(`type="static" `)
	mpd.WriteString(fmt.Sprintf(`minBufferTime="PT%.1fS" `, p.config.SegmentDuration))
	mpd.WriteString(`profiles="urn:mpeg:dash:profile:isoff-live:2011,urn:mpeg:dash:profile:cmaf:2019">` + "\n")

	mpd.WriteString(`  <Period id="0" start="PT0S">` + "\n")

	mpd.WriteString(`    <AdaptationSet mimeType="video/mp4" segmentAlignment="true" `)
	mpd.WriteString(`startWithSAP="1" contentType="video">` + "\n")

	for _, preset := range p.config.Presets {
		bandwidth := config.ParseBitrate(preset.VideoBitrate)
		mpd.WriteString(fmt.Sprintf(
			`      <Representation id="%s" bandwidth="%d" width="%d" height="%d" `+
				`codecs="avc1.%s" frameRate="%.0f">`+"\n",
			preset.Name, bandwidth, preset.Width, preset.Height,
			config.GetAVCCodecString(preset.Profile, preset.Level),
			preset.FrameRate,
		))
		mpd.WriteString(fmt.Sprintf(`        <BaseURL>%s/</BaseURL>`+"\n", preset.Name))
		mpd.WriteString(`        <SegmentTemplate initialization="init.mp4" `)
		mpd.WriteString(`media="seg_$Number%05d$.m4s" startNumber="0" `)
		mpd.WriteString(fmt.Sprintf(`timescale="1000" duration="%d"/>`+"\n",
			int(p.config.SegmentDuration*1000)))
		mpd.WriteString(`      </Representation>` + "\n")
	}

	mpd.WriteString(`    </AdaptationSet>` + "\n")

	mpd.WriteString(`    <AdaptationSet mimeType="audio/mp4" segmentAlignment="true" `)
	mpd.WriteString(`startWithSAP="1" contentType="audio" lang="en">` + "\n")
	mpd.WriteString(`      <Representation id="audio" bandwidth="128000" codecs="mp4a.40.2">` + "\n")
	mpd.WriteString(`        <SegmentTemplate initialization="init_audio.mp4" `)
	mpd.WriteString(`media="audio_$Number%05d$.m4s" startNumber="0" `)
	mpd.WriteString(fmt.Sprintf(`timescale="1000" duration="%d"/>`+"\n",
		int(p.config.SegmentDuration*1000)))
	mpd.WriteString(`      </Representation>` + "\n")
	mpd.WriteString(`    </AdaptationSet>` + "\n")

	mpd.WriteString(`  </Period>` + "\n")
	mpd.WriteString(`</MPD>` + "\n")

	return os.WriteFile(manifestPath, []byte(mpd.String()), 0644)
}

func (p *CMAFPackager) createKeyInfoFile(outputDir string) string {
	if p.config.DRMConfig == nil {
		return ""
	}

	drm := p.config.DRMConfig
	keyInfoPath := filepath.Join(outputDir, "key_info.txt")
	keyPath := filepath.Join(outputDir, "encryption.key")

	keyBytes := make([]byte, 16)
	if len(drm.Key) >= 32 {
		for i := 0; i < 16; i++ {
			fmt.Sscanf(drm.Key[i*2:i*2+2], "%02x", &keyBytes[i])
		}
	}
	os.WriteFile(keyPath, keyBytes, 0600)

	var content strings.Builder
	content.WriteString(drm.FairPlayLicenseURL + "\n")
	content.WriteString(keyPath + "\n")
	if drm.FairPlayIV != "" {
		content.WriteString(drm.FairPlayIV)
	}
	os.WriteFile(keyInfoPath, []byte(content.String()), 0644)

	return keyInfoPath
}

// GetConfig returns the packager configuration.
func (p *CMAFPackager) GetConfig() *CMAFConfig {
	return p.config
}

// SupportsShaka returns true if Shaka Packager is available.
func (p *CMAFPackager) SupportsShaka() bool {
	return p.shakaPath != "" && !p.useFallback
}

