package playlist

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amillerrr/hls-pipeline/internal/config"
)

// Generator is the interface for playlist generators.
type Generator interface {
	// GenerateMasterPlaylist generates a master playlist.
	GenerateMasterPlaylist(outputDir string, presets []config.PresetConfig) error
	// GenerateVariantPlaylist generates a variant playlist.
	GenerateVariantPlaylist(outputDir string, segments []SegmentInfo) error
}

// SegmentInfo represents information about a media segment.
type SegmentInfo struct {
	URI           string     `json:"uri"`
	Duration      float64    `json:"duration"`
	SequenceNum   int        `json:"sequenceNum"`
	ByteRange     *ByteRange `json:"byteRange,omitempty"`
	ProgramDate   *time.Time `json:"programDate,omitempty"`
	Parts         []PartInfo `json:"parts,omitempty"`
	Discontinuity bool       `json:"discontinuity,omitempty"`
	Title         string     `json:"title,omitempty"`
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
	Type      string     `json:"type"` // PART, MAP
	URI       string     `json:"uri"`
	ByteRange *ByteRange `json:"byteRange,omitempty"`
}

// RenditionReport represents the EXT-X-RENDITION-REPORT tag for LL-HLS.
type RenditionReport struct {
	URI      string `json:"uri"`
	LastMSN  int    `json:"lastMsn"`
	LastPart int    `json:"lastPart,omitempty"`
}

// StreamInfo contains stream information for master playlist.
type StreamInfo struct {
	Name         string  `json:"name"`
	Bandwidth    int64   `json:"bandwidth"`
	AvgBandwidth int64   `json:"avgBandwidth,omitempty"`
	Resolution   string  `json:"resolution,omitempty"`
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	FrameRate    float64 `json:"frameRate,omitempty"`
	Codecs       string  `json:"codecs"`
	PlaylistURI  string  `json:"playlistUri"`
	Audio        string  `json:"audio,omitempty"`
	Video        string  `json:"video,omitempty"`
	Subtitles    string  `json:"subtitles,omitempty"`
	ClosedCaptions string `json:"closedCaptions,omitempty"`
}

// AudioRendition represents an audio rendition in master playlist.
type AudioRendition struct {
	GroupID    string `json:"groupId"`
	Name       string `json:"name"`
	Language   string `json:"language,omitempty"`
	Channels   string `json:"channels,omitempty"`
	URI        string `json:"uri,omitempty"`
	Default    bool   `json:"default,omitempty"`
	Autoselect bool   `json:"autoselect,omitempty"`
}

// HLSConfig contains configuration for HLS playlist generation.
type HLSConfig struct {
	Version           int     `json:"version"`
	TargetDuration    float64 `json:"targetDuration"`
	PlaylistType      string  `json:"playlistType"` // VOD, EVENT
	IndependentSegs   bool    `json:"independentSegments"`
	InitSegmentURI    string  `json:"initSegmentUri,omitempty"`
	MediaSequence     int     `json:"mediaSequence"`
	DiscontinuitySeq  int     `json:"discontinuitySequence"`
	AudioGroupID      string  `json:"audioGroupId,omitempty"`
}

// DefaultHLSConfig returns the default HLS configuration.
func DefaultHLSConfig() *HLSConfig {
	return &HLSConfig{
		Version:         7,
		TargetDuration:  6.0,
		PlaylistType:    "VOD",
		IndependentSegs: true,
		MediaSequence:   0,
	}
}

// HLSGenerator generates HLS playlists.
type HLSGenerator struct {
	config *HLSConfig
}

// NewHLSGenerator creates a new HLS playlist generator.
func NewHLSGenerator(cfg *HLSConfig) *HLSGenerator {
	if cfg == nil {
		cfg = DefaultHLSConfig()
	}
	return &HLSGenerator{config: cfg}
}

// GenerateMasterPlaylist generates an HLS master playlist.
func (g *HLSGenerator) GenerateMasterPlaylist(outputDir string, presets []config.PresetConfig) error {
	masterPath := filepath.Join(outputDir, "master.m3u8")
	return GenerateMasterPlaylist(masterPath, presets)
}

// GenerateVariantPlaylist generates an HLS variant (media) playlist.
func (g *HLSGenerator) GenerateVariantPlaylist(outputDir string, segments []SegmentInfo) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString(fmt.Sprintf("#EXT-X-VERSION:%d\n", g.config.Version))
	playlist.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%.0f\n", g.config.TargetDuration))
	playlist.WriteString(fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", g.config.MediaSequence))

	if g.config.PlaylistType != "" {
		playlist.WriteString(fmt.Sprintf("#EXT-X-PLAYLIST-TYPE:%s\n", g.config.PlaylistType))
	}

	if g.config.IndependentSegs {
		playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
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
				seg.ProgramDate.Format(time.RFC3339Nano)))
		}

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

// GenerateMasterPlaylist generates a master playlist from presets.
// This is a standalone function for backward compatibility.
func GenerateMasterPlaylist(outputPath string, presets []config.PresetConfig) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:7\n")
	playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n\n")

	// Audio rendition
	playlist.WriteString("#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"English\",")
	playlist.WriteString("LANGUAGE=\"en\",DEFAULT=YES,AUTOSELECT=YES,CHANNELS=\"2\"\n\n")

	// Stream variants
	for _, preset := range presets {
		bandwidth := preset.Bandwidth()
		avgBandwidth := int64(float64(bandwidth) * 0.9)
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

	return os.WriteFile(outputPath, []byte(playlist.String()), 0644)
}

// GenerateMasterPlaylistWithStreams generates a master playlist from stream info.
func GenerateMasterPlaylistWithStreams(outputPath string, streams []StreamInfo, audioRenditions []AudioRendition) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:7\n")
	playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n\n")

	// Audio renditions
	for _, audio := range audioRenditions {
		playlist.WriteString(fmt.Sprintf(
			"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"%s\",NAME=\"%s\"",
			audio.GroupID, audio.Name,
		))
		if audio.Language != "" {
			playlist.WriteString(fmt.Sprintf(",LANGUAGE=\"%s\"", audio.Language))
		}
		if audio.Default {
			playlist.WriteString(",DEFAULT=YES")
		}
		if audio.Autoselect {
			playlist.WriteString(",AUTOSELECT=YES")
		}
		if audio.Channels != "" {
			playlist.WriteString(fmt.Sprintf(",CHANNELS=\"%s\"", audio.Channels))
		}
		if audio.URI != "" {
			playlist.WriteString(fmt.Sprintf(",URI=\"%s\"", audio.URI))
		}
		playlist.WriteString("\n")
	}

	playlist.WriteString("\n")

	// Stream variants
	for _, stream := range streams {
		playlist.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d", stream.Bandwidth))

		if stream.AvgBandwidth > 0 {
			playlist.WriteString(fmt.Sprintf(",AVERAGE-BANDWIDTH=%d", stream.AvgBandwidth))
		}
		if stream.Resolution != "" {
			playlist.WriteString(fmt.Sprintf(",RESOLUTION=%s", stream.Resolution))
		} else if stream.Width > 0 && stream.Height > 0 {
			playlist.WriteString(fmt.Sprintf(",RESOLUTION=%dx%d", stream.Width, stream.Height))
		}
		if stream.Codecs != "" {
			playlist.WriteString(fmt.Sprintf(",CODECS=\"%s\"", stream.Codecs))
		}
		if stream.FrameRate > 0 {
			playlist.WriteString(fmt.Sprintf(",FRAME-RATE=%.3f", stream.FrameRate))
		}
		if stream.Audio != "" {
			playlist.WriteString(fmt.Sprintf(",AUDIO=\"%s\"", stream.Audio))
		}
		if stream.Subtitles != "" {
			playlist.WriteString(fmt.Sprintf(",SUBTITLES=\"%s\"", stream.Subtitles))
		}
		if stream.ClosedCaptions != "" {
			playlist.WriteString(fmt.Sprintf(",CLOSED-CAPTIONS=\"%s\"", stream.ClosedCaptions))
		}

		playlist.WriteString("\n")
		playlist.WriteString(stream.PlaylistURI + "\n")
	}

	return os.WriteFile(outputPath, []byte(playlist.String()), 0644)
}

// GenerateIFramePlaylist generates an I-frame playlist for trick play.
func GenerateIFramePlaylist(outputPath string, bandwidth int64, codecs, uri string, resolution string) error {
	var playlist strings.Builder

	playlist.WriteString("#EXTM3U\n")
	playlist.WriteString("#EXT-X-VERSION:7\n")
	playlist.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n\n")

	playlist.WriteString(fmt.Sprintf(
		"#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=%d,CODECS=\"%s\",RESOLUTION=%s,URI=\"%s\"\n",
		bandwidth, codecs, resolution, uri,
	))

	return os.WriteFile(outputPath, []byte(playlist.String()), 0644)
}
