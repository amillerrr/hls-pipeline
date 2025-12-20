package transcoder

import (
	"fmt"
	"strings"

	"github.com/amillerrr/hls-pipeline/pkg/models"
)

// Preset defines video encoding parameters for a quality level.
type Preset struct {
	Name      string
	Width     int
	Height    int
	Bitrate   string
	MaxRate   string
	BufSize   string
	AudioBPS  string
	Bandwidth int
}

// DefaultPresets defines the standard quality levels for HLS output.
var DefaultPresets = []Preset{
	{"1080p", 1920, 1080, "5M", "5.5M", "7.5M", "192k", 5500000},
	{"720p", 1280, 720, "2.5M", "2.75M", "5M", "128k", 2750000},
	{"480p", 854, 480, "1M", "1.1M", "2M", "96k", 1100000},
}

// ToModelPresets converts transcoder presets to model presets for storage.
func ToModelPresets(presets []Preset) []models.QualityPreset {
	result := make([]models.QualityPreset, len(presets))
	for i, p := range presets {
		result[i] = models.QualityPreset{
			Name:    p.Name,
			Width:   p.Width,
			Height:  p.Height,
			Bitrate: p.Bandwidth,
		}
	}
	return result
}

// BuildFilterComplex generates the FFmpeg filter_complex string for multi-resolution output.
func BuildFilterComplex(presets []Preset) string {
	n := len(presets)
	if n == 0 {
		return ""
	}

	// Build split outputs: [v1][v2][v3]...
	var splitOutputs strings.Builder
	for i := 1; i <= n; i++ {
		splitOutputs.WriteString(fmt.Sprintf("[v%d]", i))
	}

	// Build the complete filter complex
	var filter strings.Builder
	filter.WriteString(fmt.Sprintf("[0:v]split=%d%s;", n, splitOutputs.String()))

	// Build scale filters for each preset
	for i, preset := range presets {
		filter.WriteString(fmt.Sprintf("[v%d]scale=%d:%d[v%dout]",
			i+1, preset.Width, preset.Height, i+1))
		if i < n-1 {
			filter.WriteString(";")
		}
	}

	return filter.String()
}

// GetPresetByHeight returns the preset matching the given height, or nil if not found.
func GetPresetByHeight(presets []Preset, height int) *Preset {
	for i := range presets {
		if presets[i].Height == height {
			return &presets[i]
		}
	}
	return nil
}

// GetPresetByName returns the preset matching the given name, or nil if not found.
func GetPresetByName(presets []Preset, name string) *Preset {
	for i := range presets {
		if presets[i].Name == name {
			return &presets[i]
		}
	}
	return nil
}
