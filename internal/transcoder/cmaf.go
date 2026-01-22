package transcoder

type CMAFConfig struct {
	SegmentDuration  float64 // typically 2-6 seconds
	FragmentDuration float64 // typically 0.5-2 seconds for LL-HLS
	GenerateHLS      bool
	GenerateDASH     bool
	EnableLowLatency bool
}

// TranscodeToCMAF produces CMAF-compliant fragmented MP4 segments
func (t *Transcoder) TranscodeToCMAF(ctx context.Context, videoID, inputPath, outputDir string, cfg CMAFConfig) error {
	ctx, span := tracer.Start(ctx, "transcode-cmaf")
	defer span.End()

	// Step 1: Transcode to fragmented MP4 (fMP4)
	for _, preset := range t.config.Presets {
		outPath := filepath.Join(outputDir, preset.Name)
		if err := os.MkdirAll(outPath, 0755); err != nil {
			return err
		}

		args := []string{
			"-i", inputPath,
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-profile:v", "high",
			"-level", "4.2",
			"-b:v", preset.Bitrate,
			"-maxrate", preset.MaxRate,
			"-bufsize", preset.BufSize,
			"-vf", fmt.Sprintf("scale=%d:%d", preset.Width, preset.Height),
			"-c:a", "aac",
			"-b:a", preset.AudioBPS,
			"-ar", "48000",
			// CMAF-specific flags
			"-movflags", "cmaf+separate_moof+default_base_moof+skip_trailer",
			"-frag_duration", fmt.Sprintf("%f", cfg.FragmentDuration),
			"-min_frag_duration", fmt.Sprintf("%f", cfg.FragmentDuration),
			// Output initialization segment and media segments
			"-f", "mp4",
			"-init_seg_name", "init.mp4",
			"-media_seg_name", "seg_$Number$.m4s",
			filepath.Join(outPath, "stream.mp4"),
		}

		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.config.Logger.ErrorContext(ctx, "CMAF transcode failed",
				"preset", preset.Name,
				"error", err,
				"output", string(output))
			return fmt.Errorf("CMAF transcode failed for %s: %w", preset.Name, err)
		}
	}

	// Step 2: Generate manifests using Shaka Packager
	if err := t.generateCMAFManifests(ctx, outputDir, cfg); err != nil {
		return err
	}

	return nil
}

func (t *Transcoder) generateCMAFManifests(ctx context.Context, outputDir string, cfg CMAFConfig) error {
	args := []string{}

	// Add input streams
	for _, preset := range t.config.Presets {
		streamPath := filepath.Join(outputDir, preset.Name, "stream.mp4")
		args = append(args,
			fmt.Sprintf("in=%s,stream=video,init_segment=%s/%s/init.mp4,segment_template=%s/%s/seg_$Number$.m4s",
				streamPath, outputDir, preset.Name, outputDir, preset.Name))
	}

	// Audio (take from highest quality)
	args = append(args,
		fmt.Sprintf("in=%s,stream=audio,init_segment=%s/audio/init.mp4,segment_template=%s/audio/seg_$Number$.m4s",
			filepath.Join(outputDir, "1080p", "stream.mp4"), outputDir, outputDir))

	// Output manifests
	if cfg.GenerateHLS {
		args = append(args,
			"--hls_master_playlist_output", filepath.Join(outputDir, "master.m3u8"),
			"--hls_playlist_type", "VOD")

		if cfg.EnableLowLatency {
			args = append(args,
				"--default_text_language", "en",
				"--generate_static_live_mpd")
		}
	}

	if cfg.GenerateDASH {
		args = append(args,
			"--mpd_output", filepath.Join(outputDir, "manifest.mpd"),
			"--generate_static_mpd")
	}

	cmd := exec.CommandContext(ctx, "packager", args...)
	return cmd.Run()
}
