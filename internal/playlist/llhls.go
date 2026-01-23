package playlist

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amillerrr/hls-pipeline/internal/config"
)

// LLHLSConfig contains configuration for LL-HLS playlist generation.
type LLHLSConfig struct {
	Version          int     `json:"version"`
	TargetDuration   float64 `json:"targetDuration"`
	PartTarget       float64 `json:"partTarget"`
	PartHoldBack     float64 `json:"partHoldBack"`
	CanSkipUntil     float64 `json:"canSkipUntil"`
	CanBlockReload   bool    `json:"canBlockReload"`
	PlaylistType     string  `json:"playlistType"` // VOD, EVENT, or empty for live
	InitSegmentURI   string  `json:"initSegmentUri,omitempty"`
	MediaSequence    int     `json:"mediaSequence"`
	PartSequence     int     `json:"partSequence,omitempty"`
}

// DefaultLLHLSConfig returns the default LL-HLS configuration.
func DefaultLLHLSConfig() *LLHLSConfig {
	return &LLHLSConfig{
		Version:        9,
		TargetDuration: 4.0,
		PartTarget:     0.5,
		PartHoldBack:   1.5,
		CanSkipUntil:   12.0,
		CanBlockReload: true,
		PlaylistType:   "VOD",
	}
}

// LLHLSGenerator generates Low-Latency HLS playlists.
type LLHLSGenerator struct {
	config *LLHLSConfig
}

// NewLLHLSGenerator creates a new LL-HLS playlist generator.
func NewLLHLSGenerator(cfg *LLHLSConfig) *LLHLSGenerator {
	if cfg == nil {
		cfg = DefaultLLHLSConfig()
	}

	// Validate and adjust config
	if cfg.PartHoldBack < cfg.PartTarget*3 {
		cfg.PartHoldBack = cfg.PartTarget * 3
	}
	if cfg.CanSkipUntil < cfg.TargetDuration*6 {
		cfg.CanSkipUntil = cfg.TargetDuration * 6
	}

	return &LLHLSGenerator{config: cfg}
}

// GenerateMasterPlaylist generates an LL-HLS master playlist.
func (g *LLHLSGenerator) GenerateMasterPlaylist(outputDir string, presets []config.PresetConfig) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString(fmt.Sprintf("#EXT-X-VERSION:%d\n", g.config.Version))
	playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n\n")

	// Audio rendition
	playlist.WriteString("#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"English\",")
	playlist.WriteString("LANGUAGE=\"en\",DEFAULT=YES,AUTOSELECT=YES,CHANNELS=\"2\"\n\n")

	// Stream variants with LL-HLS support
	for _, preset := range presets {
		bandwidth := preset.PeakBandwidth()
		avgBandwidth := preset.Bandwidth()
		codecs := preset.CodecString()

		playlist.WriteString(fmt.Sprintf(
			"#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"%s\",FRAME-RATE=%.3f,AUDIO=\"audio\"\n",
			bandwidth,
			avgBandwidth,
			preset.Width,
			preset.Height,
			codecs,
			preset.FrameRate,
		))
		playlist.WriteString(fmt.Sprintf("%s/playlist.m3u8\n", preset.Name))
	}

	// I-Frame playlists for trick play
	playlist.WriteString("\n")
	for _, preset := range presets {
		iframeBandwidth := int64(float64(preset.Bandwidth()) * 0.1)
		codecs := config.GetAVCCodecString(preset.Profile, preset.Level)

		playlist.WriteString(fmt.Sprintf(
			"#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=%d,CODECS=\"%s\",RESOLUTION=%dx%d,URI=\"%s/iframe.m3u8\"\n",
			iframeBandwidth,
			codecs,
			preset.Width,
			preset.Height,
			preset.Name,
		))
	}

	masterPath := filepath.Join(outputDir, "master.m3u8")
	return os.WriteFile(masterPath, []byte(playlist.String()), 0644)
}

// GenerateVariantPlaylist generates an LL-HLS variant (media) playlist.
func (g *LLHLSGenerator) GenerateVariantPlaylist(outputDir string, segments []SegmentInfo) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString(fmt.Sprintf("#EXT-X-VERSION:%d\n", g.config.Version))
	playlist.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%.0f\n", math.Ceil(g.config.TargetDuration)))
	playlist.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", g.config.MediaSequence))

	// LL-HLS specific tags
	playlist.WriteString(fmt.Sprintf("#EXT-X-PART-INF:PART-TARGET=%.6f\n", g.config.PartTarget))

	if g.config.CanBlockReload {
		playlist.WriteString("#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES")
		playlist.WriteString(fmt.Sprintf(",PART-HOLD-BACK=%.6f", g.config.PartHoldBack))
		if g.config.CanSkipUntil > 0 {
			playlist.WriteString(fmt.Sprintf(",CAN-SKIP-UNTIL=%.6f", g.config.CanSkipUntil))
		}
		playlist.WriteString("\n")
	}

	if g.config.PlaylistType != "" {
		playlist.WriteString(fmt.Sprintf("#EXT-X-PLAYLIST-TYPE:%s\n", g.config.PlaylistType))
	}

	playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")

	if g.config.InitSegmentURI != "" {
		playlist.WriteString(fmt.Sprintf("#EXT-X-MAP:URI=\"%s\"\n", g.config.InitSegmentURI))
	}

	playlist.WriteString("\n")

	// Segments with partial segments
	for _, seg := range segments {
		if seg.Discontinuity {
			playlist.WriteString("#EXT-X-DISCONTINUITY\n")
		}

		if seg.ProgramDate != nil {
			playlist.WriteString(fmt.Sprintf("#EXT-X-PROGRAM-DATE-TIME:%s\n",
				seg.ProgramDate.Format(time.RFC3339Nano)))
		}

		// Write partial segments first
		for _, part := range seg.Parts {
			g.writePartTag(&playlist, &part)
		}

		// Then the full segment
		playlist.WriteString(fmt.Sprintf("#EXTINF:%.6f,\n", seg.Duration))

		if seg.ByteRange != nil {
			playlist.WriteString(fmt.Sprintf("#EXT-X-BYTERANGE:%d@%d\n",
				seg.ByteRange.Length, seg.ByteRange.Offset))
		}

		playlist.WriteString(seg.URI + "\n")
	}

	if g.config.PlaylistType == "VOD" {
		playlist.WriteString("#EXT-X-ENDLIST\n")
	}

	playlistPath := filepath.Join(outputDir, "playlist.m3u8")
	return os.WriteFile(playlistPath, []byte(playlist.String()), 0644)
}

// writePartTag writes an EXT-X-PART tag.
func (g *LLHLSGenerator) writePartTag(sb *strings.Builder, part *PartInfo) {
	sb.WriteString(fmt.Sprintf("#EXT-X-PART:DURATION=%.6f,URI=\"%s\"", part.Duration, part.URI))

	if part.Independent {
		sb.WriteString(",INDEPENDENT=YES")
	}

	if part.ByteRange != nil {
		sb.WriteString(fmt.Sprintf(",BYTERANGE=\"%d@%d\"", part.ByteRange.Length, part.ByteRange.Offset))
	}

	if part.GAP {
		sb.WriteString(",GAP=YES")
	}

	sb.WriteString("\n")
}

// GeneratePreloadHint generates the EXT-X-PRELOAD-HINT tag.
func (g *LLHLSGenerator) GeneratePreloadHint(hint *PreloadHint) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("#EXT-X-PRELOAD-HINT:TYPE=%s,URI=\"%s\"", hint.Type, hint.URI))

	if hint.ByteRange != nil {
		sb.WriteString(fmt.Sprintf(",BYTERANGE-START=%d", hint.ByteRange.Offset))
		if hint.ByteRange.Length > 0 {
			sb.WriteString(fmt.Sprintf(",BYTERANGE-LENGTH=%d", hint.ByteRange.Length))
		}
	}

	sb.WriteString("\n")
	return sb.String()
}

// GenerateRenditionReport generates the EXT-X-RENDITION-REPORT tag.
func (g *LLHLSGenerator) GenerateRenditionReport(report *RenditionReport) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("#EXT-X-RENDITION-REPORT:URI=\"%s\",LAST-MSN=%d", report.URI, report.LastMSN))

	if report.LastPart >= 0 {
		sb.WriteString(fmt.Sprintf(",LAST-PART=%d", report.LastPart))
	}

	sb.WriteString("\n")
	return sb.String()
}

// GenerateLivePlaylist generates a live LL-HLS playlist with preload hints and rendition reports.
func (g *LLHLSGenerator) GenerateLivePlaylist(
	outputDir string,
	segments []SegmentInfo,
	preloadHint *PreloadHint,
	renditionReports []RenditionReport,
) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString(fmt.Sprintf("#EXT-X-VERSION:%d\n", g.config.Version))
	playlist.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%.0f\n", math.Ceil(g.config.TargetDuration)))
	playlist.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", g.config.MediaSequence))
	playlist.WriteString(fmt.Sprintf("#EXT-X-PART-INF:PART-TARGET=%.6f\n", g.config.PartTarget))

	// Server control for live
	playlist.WriteString("#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES")
	playlist.WriteString(fmt.Sprintf(",PART-HOLD-BACK=%.6f", g.config.PartHoldBack))
	if g.config.CanSkipUntil > 0 {
		playlist.WriteString(fmt.Sprintf(",CAN-SKIP-UNTIL=%.6f", g.config.CanSkipUntil))
	}
	playlist.WriteString("\n")

	playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")

	if g.config.InitSegmentURI != "" {
		playlist.WriteString(fmt.Sprintf("#EXT-X-MAP:URI=\"%s\"\n", g.config.InitSegmentURI))
	}

	playlist.WriteString("\n")

	// Segments
	for _, seg := range segments {
		if seg.Discontinuity {
			playlist.WriteString("#EXT-X-DISCONTINUITY\n")
		}

		if seg.ProgramDate != nil {
			playlist.WriteString(fmt.Sprintf("#EXT-X-PROGRAM-DATE-TIME:%s\n",
				seg.ProgramDate.Format(time.RFC3339Nano)))
		}

		for _, part := range seg.Parts {
			g.writePartTag(&playlist, &part)
		}

		playlist.WriteString(fmt.Sprintf("#EXTINF:%.6f,\n", seg.Duration))
		playlist.WriteString(seg.URI + "\n")
	}

	// Preload hint for next part
	if preloadHint != nil {
		playlist.WriteString(g.GeneratePreloadHint(preloadHint))
	}

	// Rendition reports
	for _, report := range renditionReports {
		playlist.WriteString(g.GenerateRenditionReport(&report))
	}

	playlistPath := filepath.Join(outputDir, "playlist.m3u8")
	return os.WriteFile(playlistPath, []byte(playlist.String()), 0644)
}

// GenerateIFramePlaylist generates an I-frame only playlist for trick play.
func (g *LLHLSGenerator) GenerateIFramePlaylist(outputDir string, iframes []SegmentInfo) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString(fmt.Sprintf("#EXT-X-VERSION:%d\n", g.config.Version))
	playlist.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%.0f\n", math.Ceil(g.config.TargetDuration)))
	playlist.WriteString("#EXT-X-I-FRAMES-ONLY\n")
	playlist.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", g.config.MediaSequence))

	if g.config.PlaylistType != "" {
		playlist.WriteString(fmt.Sprintf("#EXT-X-PLAYLIST-TYPE:%s\n", g.config.PlaylistType))
	}

	if g.config.InitSegmentURI != "" {
		playlist.WriteString(fmt.Sprintf("#EXT-X-MAP:URI=\"%s\"\n", g.config.InitSegmentURI))
	}

	playlist.WriteString("\n")

	for _, iframe := range iframes {
		playlist.WriteString(fmt.Sprintf("#EXTINF:%.6f,\n", iframe.Duration))
		if iframe.ByteRange != nil {
			playlist.WriteString(fmt.Sprintf("#EXT-X-BYTERANGE:%d@%d\n",
				iframe.ByteRange.Length, iframe.ByteRange.Offset))
		}
		playlist.WriteString(iframe.URI + "\n")
	}

	if g.config.PlaylistType == "VOD" {
		playlist.WriteString("#EXT-X-ENDLIST\n")
	}

	playlistPath := filepath.Join(outputDir, "iframe.m3u8")
	return os.WriteFile(playlistPath, []byte(playlist.String()), 0644)
}

// CalculatePartHoldBack calculates the optimal PART-HOLD-BACK value.
func CalculatePartHoldBack(partDuration float64) float64 {
	// HLS spec recommends at least 3x PART-TARGET
	return partDuration * 3
}

// CalculateCanSkipUntil calculates the optimal CAN-SKIP-UNTIL value.
func CalculateCanSkipUntil(targetDuration float64, playlistLength int) float64 {
	// HLS spec recommends at least 6x target duration
	return targetDuration * float64(playlistLength)
}
