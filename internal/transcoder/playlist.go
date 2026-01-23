// Package transcoder provides video transcoding functionality for the HLS pipeline.
package transcoder

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amillerrr/hls-pipeline/internal/config"
)

// SegmentInfo represents information about a media segment.
type SegmentInfo struct {
	URI           string     `json:"uri"`
	Duration      float64    `json:"duration"`
	SequenceNum   int        `json:"sequenceNum"`
	ByteRange     *ByteRange `json:"byteRange,omitempty"`
	ProgramDate   *time.Time `json:"programDate,omitempty"`
	Parts         []PartInfo `json:"parts,omitempty"`
	Discontinuity bool       `json:"discontinuity,omitempty"`
}

// PartInfo represents information about a partial segment (LL-HLS).
type PartInfo struct {
	URI         string     `json:"uri"`
	Duration    float64    `json:"duration"`
	Independent bool       `json:"independent"`
	ByteRange   *ByteRange `json:"byteRange,omitempty"`
	GAP         bool       `json:"gap,omitempty"`
}

// ByteRange represents a byte range for byte-range addressing.
type ByteRange struct {
	Length int64 `json:"length"`
	Offset int64 `json:"offset"`
}

// PreloadHint represents the EXT-X-PRELOAD-HINT tag.
type PreloadHint struct {
	Type      string     `json:"type"`
	URI       string     `json:"uri"`
	ByteRange *ByteRange `json:"byteRange,omitempty"`
}

// RenditionReport represents the EXT-X-RENDITION-REPORT tag.
type RenditionReport struct {
	URI      string `json:"uri"`
	LastMSN  int    `json:"lastMsn"`
	LastPart int    `json:"lastPart,omitempty"`
}

// LLHLSPlaylistConfig contains configuration for LL-HLS playlist generation.
type LLHLSPlaylistConfig struct {
	TargetDuration float64 `json:"targetDuration"`
	PartTarget     float64 `json:"partTarget"`
	PartHoldBack   float64 `json:"partHoldBack"`
	CanSkipUntil   float64 `json:"canSkipUntil"`
	CanBlockReload bool    `json:"canBlockReload"`
	PlaylistType   string  `json:"playlistType"`
	InitSegmentURI string  `json:"initSegmentUri,omitempty"`
	MediaSequence  int     `json:"mediaSequence"`
}

// LLHLSPlaylistGenerator generates LL-HLS compliant playlists.
type LLHLSPlaylistGenerator struct {
	config *LLHLSPlaylistConfig
}

// NewLLHLSPlaylistGenerator creates a new playlist generator.
func NewLLHLSPlaylistGenerator(cfg *LLHLSPlaylistConfig) *LLHLSPlaylistGenerator {
	if cfg == nil {
		cfg = &LLHLSPlaylistConfig{
			TargetDuration: 4.0,
			PartTarget:     0.5,
			PartHoldBack:   1.5,
			CanSkipUntil:   12.0,
			CanBlockReload: true,
			PlaylistType:   "VOD",
		}
	}
	return &LLHLSPlaylistGenerator{config: cfg}
}

// GenerateVariantPlaylist generates an LL-HLS variant playlist.
func (g *LLHLSPlaylistGenerator) GenerateVariantPlaylist(
	segments []SegmentInfo,
	preloadHint *PreloadHint,
	renditionReports []RenditionReport,
) string {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:9\n")
	playlist.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n",
		int(math.Ceil(g.config.TargetDuration))))

	playlist.WriteString(fmt.Sprintf("#EXT-X-PART-INF:PART-TARGET=%.5f\n",
		g.config.PartTarget))

	var serverControl strings.Builder
	serverControl.WriteString("#EXT-X-SERVER-CONTROL:")
	if g.config.CanBlockReload {
		serverControl.WriteString("CAN-BLOCK-RELOAD=YES,")
	}
	serverControl.WriteString(fmt.Sprintf("PART-HOLD-BACK=%.5f", g.config.PartHoldBack))
	if g.config.CanSkipUntil > 0 {
		serverControl.WriteString(fmt.Sprintf(",CAN-SKIP-UNTIL=%.1f", g.config.CanSkipUntil))
	}
	playlist.WriteString(serverControl.String() + "\n")

	playlist.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", g.config.MediaSequence))

	if g.config.PlaylistType != "" {
		playlist.WriteString(fmt.Sprintf("#EXT-X-PLAYLIST-TYPE:%s\n", g.config.PlaylistType))
	}

	if g.config.InitSegmentURI != "" {
		playlist.WriteString(fmt.Sprintf("#EXT-X-MAP:URI=\"%s\"\n", g.config.InitSegmentURI))
	}

	playlist.WriteString("\n")

	for _, seg := range segments {
		if seg.Discontinuity {
			playlist.WriteString("#EXT-X-DISCONTINUITY\n")
		}

		if seg.ProgramDate != nil {
			playlist.WriteString(fmt.Sprintf("#EXT-X-PROGRAM-DATE-TIME:%s\n",
				seg.ProgramDate.Format("2006-01-02T15:04:05.000Z")))
		}

		for _, part := range seg.Parts {
			playlist.WriteString(g.formatPart(part))
		}

		playlist.WriteString(fmt.Sprintf("#EXTINF:%.5f,\n", seg.Duration))
		if seg.ByteRange != nil {
			playlist.WriteString(fmt.Sprintf("#EXT-X-BYTERANGE:%d@%d\n",
				seg.ByteRange.Length, seg.ByteRange.Offset))
		}
		playlist.WriteString(seg.URI + "\n")
	}

	if preloadHint != nil {
		playlist.WriteString(g.formatPreloadHint(preloadHint))
	}

	for _, report := range renditionReports {
		playlist.WriteString(g.formatRenditionReport(report))
	}

	if g.config.PlaylistType == "VOD" {
		playlist.WriteString("#EXT-X-ENDLIST\n")
	}

	return playlist.String()
}

func (g *LLHLSPlaylistGenerator) formatPart(part PartInfo) string {
	var tag strings.Builder
	tag.WriteString(fmt.Sprintf("#EXT-X-PART:DURATION=%.5f,URI=\"%s\"",
		part.Duration, part.URI))

	if part.Independent {
		tag.WriteString(",INDEPENDENT=YES")
	}
	if part.GAP {
		tag.WriteString(",GAP=YES")
	}
	if part.ByteRange != nil {
		tag.WriteString(fmt.Sprintf(",BYTERANGE=%d@%d",
			part.ByteRange.Length, part.ByteRange.Offset))
	}
	tag.WriteString("\n")
	return tag.String()
}

func (g *LLHLSPlaylistGenerator) formatPreloadHint(hint *PreloadHint) string {
	var tag strings.Builder
	tag.WriteString(fmt.Sprintf("#EXT-X-PRELOAD-HINT:TYPE=%s,URI=\"%s\"",
		hint.Type, hint.URI))

	if hint.ByteRange != nil {
		tag.WriteString(fmt.Sprintf(",BYTERANGE-START=%d", hint.ByteRange.Offset))
		if hint.ByteRange.Length > 0 {
			tag.WriteString(fmt.Sprintf(",BYTERANGE-LENGTH=%d", hint.ByteRange.Length))
		}
	}
	tag.WriteString("\n")
	return tag.String()
}

func (g *LLHLSPlaylistGenerator) formatRenditionReport(report RenditionReport) string {
	var tag strings.Builder
	tag.WriteString(fmt.Sprintf("#EXT-X-RENDITION-REPORT:URI=\"%s\",LAST-MSN=%d",
		report.URI, report.LastMSN))

	if report.LastPart > 0 {
		tag.WriteString(fmt.Sprintf(",LAST-PART=%d", report.LastPart))
	}
	tag.WriteString("\n")
	return tag.String()
}

// GenerateMasterPlaylist generates an HLS v9 master playlist.
func GenerateMasterPlaylist(
	outputDir string,
	presets []config.PresetConfig,
	frameRate float64,
	audioGroupID string,
) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:9\n")
	playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	playlist.WriteString("\n")

	if audioGroupID == "" {
		audioGroupID = "audio"
	}
	playlist.WriteString(fmt.Sprintf(
		"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"%s\",NAME=\"English\","+
			"LANGUAGE=\"en\",DEFAULT=YES,AUTOSELECT=YES,CHANNELS=\"2\"\n\n",
		audioGroupID))

	for _, preset := range presets {
		bandwidth := preset.PeakBandwidth()
		avgBandwidth := int64(float64(preset.Bandwidth()) * 0.9)

		if frameRate <= 0 {
			frameRate = preset.FrameRate
		}
		if frameRate <= 0 {
			frameRate = 30.0
		}

		playlist.WriteString(fmt.Sprintf(
			"#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d,"+
				"RESOLUTION=%dx%d,FRAME-RATE=%.3f,"+
				"CODECS=\"%s\",AUDIO=\"%s\"\n",
			bandwidth, avgBandwidth,
			preset.Width, preset.Height,
			frameRate,
			preset.CodecString(),
			audioGroupID,
		))
		playlist.WriteString(fmt.Sprintf("%s/playlist.m3u8\n", preset.Name))
	}

	masterPath := filepath.Join(outputDir, "master.m3u8")
	return os.WriteFile(masterPath, []byte(playlist.String()), 0644)
}

// GenerateIFramePlaylist generates an I-frame only playlist for trick play.
func GenerateIFramePlaylist(
	outputDir string,
	presetName string,
	iframes []IFrameInfo,
	initSegmentURI string,
) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:7\n")
	playlist.WriteString("#EXT-X-I-FRAMES-ONLY\n")
	playlist.WriteString("#EXT-X-TARGETDURATION:4\n")
	playlist.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")

	if initSegmentURI != "" {
		playlist.WriteString(fmt.Sprintf("#EXT-X-MAP:URI=\"%s\"\n", initSegmentURI))
	}

	playlist.WriteString("\n")

	for _, iframe := range iframes {
		playlist.WriteString(fmt.Sprintf("#EXTINF:%.5f,\n", iframe.Duration))
		playlist.WriteString(fmt.Sprintf("#EXT-X-BYTERANGE:%d@%d\n",
			iframe.ByteRange.Length, iframe.ByteRange.Offset))
		playlist.WriteString(iframe.URI + "\n")
	}

	playlist.WriteString("#EXT-X-ENDLIST\n")

	iframePath := filepath.Join(outputDir, presetName, "iframe.m3u8")
	return os.WriteFile(iframePath, []byte(playlist.String()), 0644)
}

// IFrameInfo represents information about an I-frame.
type IFrameInfo struct {
	URI       string     `json:"uri"`
	Duration  float64    `json:"duration"`
	ByteRange *ByteRange `json:"byteRange"`
}

// UpdateVariantPlaylistWithParts updates an existing variant playlist with new parts.
func UpdateVariantPlaylistWithParts(
	existingPlaylist string,
	newParts []PartInfo,
	newSegment *SegmentInfo,
	preloadHint *PreloadHint,
	mediaSequenceIncrement int,
) string {
	lines := strings.Split(existingPlaylist, "\n")
	var result strings.Builder

	insertIdx := len(lines)
	for i, line := range lines {
		if strings.HasPrefix(line, "#EXT-X-ENDLIST") {
			insertIdx = i
			break
		}
	}

	for i := 0; i < insertIdx; i++ {
		line := lines[i]

		if strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:") && mediaSequenceIncrement > 0 {
			var seq int
			fmt.Sscanf(line, "#EXT-X-MEDIA-SEQUENCE:%d", &seq)
			result.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", seq+mediaSequenceIncrement))
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-PRELOAD-HINT:") ||
			strings.HasPrefix(line, "#EXT-X-RENDITION-REPORT:") {
			continue
		}

		result.WriteString(line + "\n")
	}

	gen := NewLLHLSPlaylistGenerator(nil)
	for _, part := range newParts {
		result.WriteString(gen.formatPart(part))
	}

	if newSegment != nil {
		result.WriteString(fmt.Sprintf("#EXTINF:%.5f,\n", newSegment.Duration))
		result.WriteString(newSegment.URI + "\n")
	}

	if preloadHint != nil {
		result.WriteString(gen.formatPreloadHint(preloadHint))
	}

	return result.String()
}

// ParsePlaylistDuration calculates the total duration of a playlist.
func ParsePlaylistDuration(playlistPath string) (float64, error) {
	content, err := os.ReadFile(playlistPath)
	if err != nil {
		return 0, err
	}

	var totalDuration float64
	lines := strings.Split(string(content), "\n")

	for _, line := range lines {
		if strings.HasPrefix(line, "#EXTINF:") {
			var duration float64
			fmt.Sscanf(line, "#EXTINF:%f", &duration)
			totalDuration += duration
		}
	}

	return totalDuration, nil
}

// CreateOutputDirectories creates the output directory structure for transcoding.
func CreateOutputDirectories(hlsDir string, presets []config.PresetConfig) error {
	for _, preset := range presets {
		presetDir := filepath.Join(hlsDir, preset.Name)
		if err := os.MkdirAll(presetDir, 0755); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", preset.Name, err)
		}
	}
	return nil
}

