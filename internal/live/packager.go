package live

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
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// LivePackagerConfig contains configuration for the live packager.
type LivePackagerConfig struct {
	StreamID        string
	InputURL        string
	OutputBucket    string
	OutputPrefix    string
	SegmentDuration float64
	PartDuration    float64
	Presets         []string
	EnableLLHLS     bool
	EnableDRM       bool
	DVRWindowSize   int
	AWSRegion       string
	
	// Advanced settings
	GOPSize         int     // Keyframe interval in frames
	BufferSize      string  // FFmpeg buffer size
	MaxMuxingQueue  int     // FFmpeg max muxing queue size
}

// LivePackager handles real-time CMAF packaging of live streams.
type LivePackager struct {
	config    *LivePackagerConfig
	logger    *slog.Logger
	s3Client  *s3.Client
	
	// State
	active    atomic.Bool
	startTime time.Time
	stats     *StreamStats
	statsMu   sync.RWMutex
	
	// Processes
	ffmpegCmd   *exec.Cmd
	ffmpegPipe  io.WriteCloser
	
	// Output management
	localOutputDir string
	uploadQueue    chan string
	uploadWG       sync.WaitGroup
}

// NewLivePackager creates a new live packager.
func NewLivePackager(cfg *LivePackagerConfig, logger *slog.Logger) (*LivePackager, error) {
	// Set defaults
	if cfg.SegmentDuration == 0 {
		cfg.SegmentDuration = 4.0
	}
	if cfg.PartDuration == 0 && cfg.EnableLLHLS {
		cfg.PartDuration = 0.5
	}
	if cfg.GOPSize == 0 {
		cfg.GOPSize = int(cfg.SegmentDuration * 30) // Assuming 30fps
	}
	if cfg.BufferSize == "" {
		cfg.BufferSize = "4M"
	}
	if cfg.MaxMuxingQueue == 0 {
		cfg.MaxMuxingQueue = 1024
	}

	// Create AWS config
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.AWSRegion),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create local output directory
	localOutputDir := filepath.Join(os.TempDir(), "live", cfg.StreamID)
	if err := os.MkdirAll(localOutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	return &LivePackager{
		config:         cfg,
		logger:         logger,
		s3Client:       s3.NewFromConfig(awsCfg),
		stats:          &StreamStats{},
		localOutputDir: localOutputDir,
		uploadQueue:    make(chan string, 100),
	}, nil
}

// Start starts the live packager.
func (p *LivePackager) Start(ctx context.Context) error {
	p.startTime = time.Now()

	// Start the S3 uploader
	p.uploadWG.Add(1)
	go p.uploadWorker(ctx)

	// Build and start FFmpeg command
	args := p.buildFFmpegArgs()
	p.logger.Debug("starting ffmpeg",
		"args", strings.Join(args, " "),
	)

	p.ffmpegCmd = exec.CommandContext(ctx, "ffmpeg", args...)

	// Capture stderr for progress/errors
	stderr, err := p.ffmpegCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := p.ffmpegCmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Monitor FFmpeg output in a goroutine
	go p.monitorFFmpeg(ctx, stderr)

	// Start file watcher for new segments
	go p.watchSegments(ctx)

	// Wait for FFmpeg to exit
	err = p.ffmpegCmd.Wait()
	
	// Signal completion
	p.active.Store(false)
	close(p.uploadQueue)
	p.uploadWG.Wait()

	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("ffmpeg exited with error: %w", err)
	}

	return nil
}

// Stop stops the live packager.
func (p *LivePackager) Stop(ctx context.Context) error {
	if p.ffmpegCmd != nil && p.ffmpegCmd.Process != nil {
		// Send SIGINT for graceful shutdown
		p.ffmpegCmd.Process.Signal(os.Interrupt)

		// Wait with timeout
		done := make(chan error, 1)
		go func() {
			done <- p.ffmpegCmd.Wait()
		}()

		select {
		case <-done:
			// Process exited
		case <-time.After(10 * time.Second):
			// Force kill
			p.ffmpegCmd.Process.Kill()
		}
	}

	// Wait for uploads to complete
	p.uploadWG.Wait()

	// Cleanup local files
	os.RemoveAll(p.localOutputDir)

	return nil
}

// IsActive returns true if the packager is actively processing.
func (p *LivePackager) IsActive() bool {
	return p.active.Load()
}

// GetStats returns current stream statistics.
func (p *LivePackager) GetStats() *StreamStats {
	p.statsMu.RLock()
	defer p.statsMu.RUnlock()

	stats := *p.stats
	stats.Duration = time.Since(p.startTime)
	return &stats
}

// buildFFmpegArgs builds the FFmpeg command arguments.
func (p *LivePackager) buildFFmpegArgs() []string {
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "warning",
		"-stats",
	}

	// Input options based on protocol
	if strings.HasPrefix(p.config.InputURL, "srt://") {
		args = append(args,
			"-fflags", "+genpts+discardcorrupt",
			"-i", p.config.InputURL,
		)
	} else if strings.HasPrefix(p.config.InputURL, "rtmp://") {
		args = append(args,
			"-listen", "1",
			"-i", p.config.InputURL,
		)
	} else {
		args = append(args,
			"-i", p.config.InputURL,
		)
	}

	// Build filter complex for multi-bitrate output
	presets := p.config.Presets
	if len(presets) == 0 {
		presets = []string{"1080p", "720p", "480p"}
	}

	// Build the filter graph
	filterComplex := p.buildFilterComplex(presets)
	if filterComplex != "" {
		args = append(args, "-filter_complex", filterComplex)
	}

	// Add output streams for each preset
	for i, preset := range presets {
		res := getPresetResolution(preset)
		bitrate := getPresetBitrate(preset)

		// Map from filter output
		args = append(args, "-map", fmt.Sprintf("[v%d]", i))
		args = append(args, "-map", "0:a?")

		// Video encoding
		args = append(args,
			fmt.Sprintf("-c:v:%d", i), "libx264",
			fmt.Sprintf("-b:v:%d", i), bitrate,
			fmt.Sprintf("-maxrate:v:%d", i), bitrate,
			fmt.Sprintf("-bufsize:v:%d", i), p.config.BufferSize,
			fmt.Sprintf("-preset:%d", i), "veryfast",
			fmt.Sprintf("-profile:v:%d", i), "main",
			fmt.Sprintf("-g:%d", i), strconv.Itoa(p.config.GOPSize),
			fmt.Sprintf("-keyint_min:%d", i), strconv.Itoa(p.config.GOPSize),
			fmt.Sprintf("-sc_threshold:%d", i), "0",
		)

		// Add resolution-specific metadata
		args = append(args,
			fmt.Sprintf("-metadata:s:v:%d", i), fmt.Sprintf("title=%s", preset),
		)

		_ = res // Used in filter complex
	}

	// Audio encoding (single audio track)
	args = append(args,
		"-c:a", "aac",
		"-b:a", "128k",
		"-ac", "2",
		"-ar", "48000",
	)

	// Output format options
	segDuration := p.config.SegmentDuration

	if p.config.EnableLLHLS {
		// LL-HLS with fMP4/CMAF
		args = append(args,
			"-f", "hls",
			"-hls_time", fmt.Sprintf("%.1f", segDuration),
			"-hls_list_size", "10",
			"-hls_flags", "independent_segments+delete_segments+program_date_time",
			"-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", "init_%v.mp4",
			"-hls_segment_filename", filepath.Join(p.localOutputDir, "stream_%v_%03d.m4s"),
			"-master_pl_name", "master.m3u8",
			"-var_stream_map", p.buildVarStreamMap(presets),
		)

		// LL-HLS specific options
		if p.config.PartDuration > 0 {
			args = append(args,
				"-hls_fmp4_init_resend", "1",
				"-hls_flags", "independent_segments+delete_segments+program_date_time+split_by_time",
			)
		}
	} else {
		// Standard HLS with TS segments
		args = append(args,
			"-f", "hls",
			"-hls_time", fmt.Sprintf("%.1f", segDuration),
			"-hls_list_size", "6",
			"-hls_flags", "delete_segments+program_date_time+independent_segments",
			"-hls_segment_filename", filepath.Join(p.localOutputDir, "stream_%v_%03d.ts"),
			"-master_pl_name", "master.m3u8",
			"-var_stream_map", p.buildVarStreamMap(presets),
		)
	}

	// Output path
	args = append(args, filepath.Join(p.localOutputDir, "stream_%v.m3u8"))

	return args
}

// buildFilterComplex builds the FFmpeg filter complex for multi-bitrate encoding.
func (p *LivePackager) buildFilterComplex(presets []string) string {
	if len(presets) == 0 {
		return ""
	}

	var filters []string
	
	// Split input to multiple outputs
	splitOut := ""
	for i := range presets {
		splitOut += fmt.Sprintf("[v%d]", i)
	}
	filters = append(filters, fmt.Sprintf("[0:v]split=%d%s", len(presets), splitOut))

	// Scale each output
	for i, preset := range presets {
		res := getPresetResolution(preset)
		// Use scale filter with force_original_aspect_ratio
		filters = append(filters,
			fmt.Sprintf("[v%d]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2[v%d]",
				i, res.width, res.height, res.width, res.height, i),
		)
	}

	return strings.Join(filters, ";")
}

// buildVarStreamMap builds the variant stream mapping for HLS output.
func (p *LivePackager) buildVarStreamMap(presets []string) string {
	var parts []string
	for i := range presets {
		// Each variant has one video stream and the audio stream
		parts = append(parts, fmt.Sprintf("v:%d,a:0", i))
	}
	return strings.Join(parts, " ")
}

// monitorFFmpeg monitors FFmpeg stderr for progress and errors.
func (p *LivePackager) monitorFFmpeg(ctx context.Context, stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	frameRegex := regexp.MustCompile(`frame=\s*(\d+)`)
	fpsRegex := regexp.MustCompile(`fps=\s*(\d+\.?\d*)`)
	bitrateRegex := regexp.MustCompile(`bitrate=\s*(\d+\.?\d*)kbits/s`)
	
	for scanner.Scan() {
		line := scanner.Text()

		// Check if we've started receiving frames
		if strings.Contains(line, "frame=") && !p.active.Load() {
			p.active.Store(true)
			p.logger.Info("stream active - receiving frames")
		}

		// Parse statistics
		if matches := frameRegex.FindStringSubmatch(line); len(matches) > 1 {
			frames, _ := strconv.ParseInt(matches[1], 10, 64)
			p.statsMu.Lock()
			p.stats.FramesReceived = frames
			p.statsMu.Unlock()
		}

		if matches := fpsRegex.FindStringSubmatch(line); len(matches) > 1 {
			fps, _ := strconv.ParseFloat(matches[1], 64)
			p.statsMu.Lock()
			p.stats.InputFrameRate = fps
			p.statsMu.Unlock()
		}

		if matches := bitrateRegex.FindStringSubmatch(line); len(matches) > 1 {
			bitrate, _ := strconv.ParseFloat(matches[1], 64)
			p.statsMu.Lock()
			p.stats.Bitrate = int64(bitrate * 1000) // Convert to bps
			p.statsMu.Unlock()
		}

		// Log warnings and errors
		if strings.Contains(line, "error") || strings.Contains(line, "Error") {
			p.logger.Warn("ffmpeg error", "message", line)
		}
	}
}

// watchSegments watches for new segment files and queues them for upload.
func (p *LivePackager) watchSegments(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	uploadedFiles := make(map[string]bool)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			files, err := os.ReadDir(p.localOutputDir)
			if err != nil {
				continue
			}

			for _, file := range files {
				if file.IsDir() {
					continue
				}

				name := file.Name()
				
				// Skip if already uploaded
				if uploadedFiles[name] {
					continue
				}

				// Check if file is complete (for segments, check if a newer one exists)
				if isSegmentFile(name) {
					if !p.isSegmentComplete(name, files) {
						continue
					}
				}

				// Queue for upload
				fullPath := filepath.Join(p.localOutputDir, name)
				select {
				case p.uploadQueue <- fullPath:
					uploadedFiles[name] = true
				default:
					// Queue full, will retry next tick
				}
			}
		}
	}
}

// isSegmentComplete checks if a segment file is complete (newer segment exists).
func (p *LivePackager) isSegmentComplete(name string, files []os.DirEntry) bool {
	// Extract segment number
	re := regexp.MustCompile(`_(\d+)\.(m4s|ts)$`)
	matches := re.FindStringSubmatch(name)
	if len(matches) < 2 {
		return true // Not a numbered segment, assume complete
	}

	segNum, _ := strconv.Atoi(matches[1])
	prefix := name[:strings.LastIndex(name, "_")]
	ext := filepath.Ext(name)

	// Check if next segment exists
	nextName := fmt.Sprintf("%s_%03d%s", prefix, segNum+1, ext)
	for _, f := range files {
		if f.Name() == nextName {
			return true
		}
	}

	return false
}

// uploadWorker uploads files to S3.
func (p *LivePackager) uploadWorker(ctx context.Context) {
	defer p.uploadWG.Done()

	for {
		select {
		case filePath, ok := <-p.uploadQueue:
			if !ok {
				return
			}
			if err := p.uploadFile(ctx, filePath); err != nil {
				p.logger.Error("failed to upload file",
					"file", filePath,
					"error", err,
				)
			}
		case <-ctx.Done():
			// Drain remaining files
			for filePath := range p.uploadQueue {
				ctx2, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				p.uploadFile(ctx2, filePath)
				cancel()
			}
			return
		}
	}
}

// uploadFile uploads a single file to S3.
func (p *LivePackager) uploadFile(ctx context.Context, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	fileName := filepath.Base(filePath)
	key := fmt.Sprintf("%s/%s", p.config.OutputPrefix, fileName)

	contentType := "application/octet-stream"
	switch filepath.Ext(fileName) {
	case ".m3u8":
		contentType = "application/vnd.apple.mpegurl"
	case ".m4s":
		contentType = "video/iso.segment"
	case ".mp4":
		contentType = "video/mp4"
	case ".ts":
		contentType = "video/mp2t"
	}

	_, err = p.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:       aws.String(p.config.OutputBucket),
		Key:          aws.String(key),
		Body:         file,
		ContentType:  aws.String(contentType),
		CacheControl: aws.String(getCacheControl(fileName)),
	})

	if err != nil {
		return err
	}

	p.statsMu.Lock()
	if isSegmentFile(fileName) {
		p.stats.SegmentsCreated++
		p.stats.LastSegmentTime = time.Now()
	}
	p.statsMu.Unlock()

	p.logger.Debug("uploaded file",
		"key", key,
	)

	// Delete local file after successful upload
	os.Remove(filePath)

	return nil
}

// Helper types and functions

type resolution struct {
	width  int
	height int
}

func getPresetResolution(preset string) resolution {
	presets := map[string]resolution{
		"4k":    {3840, 2160},
		"1080p": {1920, 1080},
		"720p":  {1280, 720},
		"480p":  {854, 480},
		"360p":  {640, 360},
		"240p":  {426, 240},
	}
	if res, ok := presets[preset]; ok {
		return res
	}
	return resolution{1280, 720} // Default to 720p
}

func getPresetBitrate(preset string) string {
	presets := map[string]string{
		"4k":    "15000k",
		"1080p": "5000k",
		"720p":  "2800k",
		"480p":  "1400k",
		"360p":  "800k",
		"240p":  "400k",
	}
	if bitrate, ok := presets[preset]; ok {
		return bitrate
	}
	return "2800k" // Default
}

func isSegmentFile(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".m4s" || ext == ".ts"
}

func getCacheControl(fileName string) string {
	ext := filepath.Ext(fileName)
	switch ext {
	case ".m3u8":
		// Playlists should not be cached (or very short cache)
		return "max-age=1, s-maxage=1"
	case ".mp4":
		// Init segments can be cached longer
		return "max-age=31536000, immutable"
	case ".m4s", ".ts":
		// Media segments can be cached for a long time
		return "max-age=31536000, immutable"
	default:
		return "max-age=3600"
	}
}

