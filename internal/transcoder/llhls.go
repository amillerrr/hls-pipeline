package transcoder

type LLHLSConfig struct {
	PartDuration    float64 // 0.5-1.0 seconds recommended
	SegmentDuration float64 // 4-6 seconds
	PlaylistLength  int     // Number of segments in playlist
	PartHoldBack    float64 // 3 * PartDuration recommended
	CanBlockReload  bool    // Enable blocking playlist reload
	CanSkipUntil    float64 // Skip to most recent parts
}

func DefaultLLHLSConfig() LLHLSConfig {
	return LLHLSConfig{
		PartDuration:    0.5,
		SegmentDuration: 4.0,
		PlaylistLength:  5,
		PartHoldBack:    1.5,
		CanBlockReload:  true,
		CanSkipUntil:    12.0,
	}
}

// TranscodeToLLHLS produces LL-HLS compliant output
func (t *Transcoder) TranscodeToLLHLS(ctx context.Context, videoID, inputPath, outputDir string, cfg LLHLSConfig) error {
	ctx, span := tracer.Start(ctx, "transcode-llhls")
	defer span.End()

	for _, preset := range t.config.Presets {
		outPath := filepath.Join(outputDir, preset.Name)
		if err := os.MkdirAll(outPath, 0755); err != nil {
			return err
		}

		args := []string{
			"-i", inputPath,
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-tune", "zerolatency", // Reduce encoding latency
			"-profile:v", "main",
			"-level", "4.1",
			"-b:v", preset.Bitrate,
			"-maxrate", preset.MaxRate,
			"-bufsize", preset.BufSize,
			"-vf", fmt.Sprintf("scale=%d:%d", preset.Width, preset.Height),
			"-g", "48", // Keyframe every 2 seconds at 24fps
			"-keyint_min", "48",
			"-sc_threshold", "0",
			"-c:a", "aac",
			"-b:a", preset.AudioBPS,
			"-ar", "48000",
			"-ac", "2",
			// LL-HLS specific flags
			"-f", "hls",
			"-hls_time", fmt.Sprintf("%.0f", cfg.SegmentDuration),
			"-hls_list_size", fmt.Sprintf("%d", cfg.PlaylistLength),
			"-hls_flags", "independent_segments+program_date_time",
			"-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", "init.mp4",
			"-hls_segment_filename", filepath.Join(outPath, "seg_%d.m4s"),
			// Part configuration for LL-HLS
			"-hls_part_duration", fmt.Sprintf("%.2f", cfg.PartDuration),
			filepath.Join(outPath, "playlist.m3u8"),
		}

		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("LL-HLS transcode failed: %w\n%s", err, output)
		}
	}

	// Generate LL-HLS master playlist
	return t.generateLLHLSMasterPlaylist(outputDir, cfg)
}

func (t *Transcoder) generateLLHLSMasterPlaylist(outputDir string, cfg LLHLSConfig) error {
	var builder strings.Builder

	builder.WriteString("#EXTM3U\n")
	builder.WriteString("#EXT-X-VERSION:9\n") // Version 9 for LL-HLS
	builder.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")

	// LL-HLS server control
	builder.WriteString(fmt.Sprintf("#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=%.1f",
		cfg.PartHoldBack))
	if cfg.CanSkipUntil > 0 {
		builder.WriteString(fmt.Sprintf(",CAN-SKIP-UNTIL=%.1f", cfg.CanSkipUntil))
	}
	builder.WriteString("\n")

	for _, preset := range t.config.Presets {
		builder.WriteString(fmt.Sprintf(
			"#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"avc1.64001f,mp4a.40.2\"\n",
			preset.Bandwidth, preset.Width, preset.Height))
		builder.WriteString(fmt.Sprintf("%s/playlist.m3u8\n", preset.Name))
	}

	return os.WriteFile(
		filepath.Join(outputDir, "master.m3u8"),
		[]byte(builder.String()),
		0644)
}
