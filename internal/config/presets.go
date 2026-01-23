package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// PresetConfig defines video encoding parameters for a quality level.
// This is the SINGLE SOURCE OF TRUTH for all preset definitions.
type PresetConfig struct {
	Name         string  `json:"name" yaml:"name"`
	Width        int     `json:"width" yaml:"width"`
	Height       int     `json:"height" yaml:"height"`
	VideoBitrate string  `json:"videoBitrate" yaml:"videoBitrate"`
	AudioBitrate string  `json:"audioBitrate" yaml:"audioBitrate"`
	Profile      string  `json:"profile" yaml:"profile"`
	Level        string  `json:"level" yaml:"level"`
	MaxRate      string  `json:"maxRate" yaml:"maxRate"`
	BufSize      string  `json:"bufSize" yaml:"bufSize"`
	FrameRate    float64 `json:"frameRate" yaml:"frameRate"`
	GOPSeconds   float64 `json:"gopSeconds" yaml:"gopSeconds"`
}

// PresetCollection holds a named collection of presets.
type PresetCollection struct {
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description" yaml:"description"`
	Presets     []PresetConfig `json:"presets" yaml:"presets"`
}

// PresetsFile represents the structure of the presets configuration file.
type PresetsFile struct {
	Version     string             `json:"version" yaml:"version"`
	Collections []PresetCollection `json:"collections" yaml:"collections"`
}

// DefaultPresets returns the standard encoding ladder.
// This is used when no external configuration is provided.
func DefaultPresets() []PresetConfig {
	return []PresetConfig{
		{
			Name:         "1080p",
			Width:        1920,
			Height:       1080,
			VideoBitrate: "5000k",
			AudioBitrate: "192k",
			Profile:      "high",
			Level:        "4.2",
			MaxRate:      "5500k",
			BufSize:      "7500k",
			FrameRate:    30.0,
			GOPSeconds:   2.0,
		},
		{
			Name:         "720p",
			Width:        1280,
			Height:       720,
			VideoBitrate: "2800k",
			AudioBitrate: "128k",
			Profile:      "high",
			Level:        "4.1",
			MaxRate:      "3080k",
			BufSize:      "5600k",
			FrameRate:    30.0,
			GOPSeconds:   2.0,
		},
		{
			Name:         "480p",
			Width:        854,
			Height:       480,
			VideoBitrate: "1400k",
			AudioBitrate: "128k",
			Profile:      "main",
			Level:        "3.1",
			MaxRate:      "1540k",
			BufSize:      "2800k",
			FrameRate:    30.0,
			GOPSeconds:   2.0,
		},
		{
			Name:         "360p",
			Width:        640,
			Height:       360,
			VideoBitrate: "800k",
			AudioBitrate: "96k",
			Profile:      "main",
			Level:        "3.0",
			MaxRate:      "880k",
			BufSize:      "1600k",
			FrameRate:    30.0,
			GOPSeconds:   2.0,
		},
		{
			Name:         "240p",
			Width:        426,
			Height:       240,
			VideoBitrate: "400k",
			AudioBitrate: "64k",
			Profile:      "baseline",
			Level:        "3.0",
			MaxRate:      "440k",
			BufSize:      "800k",
			FrameRate:    30.0,
			GOPSeconds:   2.0,
		},
	}
}

// LoadPresets loads presets from a YAML or JSON file.
func LoadPresets(path string) (*PresetsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read presets file: %w", err)
	}

	var presets PresetsFile

	if strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") {
		if err := yaml.Unmarshal(data, &presets); err != nil {
			return nil, fmt.Errorf("failed to parse YAML presets: %w", err)
		}
	} else {
		if err := json.Unmarshal(data, &presets); err != nil {
			return nil, fmt.Errorf("failed to parse JSON presets: %w", err)
		}
	}

	for _, collection := range presets.Collections {
		for i := range collection.Presets {
			if err := ValidatePreset(&collection.Presets[i]); err != nil {
				return nil, fmt.Errorf("invalid preset %s in collection %s: %w",
					collection.Presets[i].Name, collection.Name, err)
			}
		}
	}

	return &presets, nil
}

// GetCollection returns a preset collection by name.
func (p *PresetsFile) GetCollection(name string) (*PresetCollection, error) {
	for i := range p.Collections {
		if p.Collections[i].Name == name {
			return &p.Collections[i], nil
		}
	}
	return nil, fmt.Errorf("preset collection %q not found", name)
}

// ValidatePreset validates a preset configuration.
func ValidatePreset(p *PresetConfig) error {
	if p.Name == "" {
		return fmt.Errorf("preset name is required")
	}
	if p.Width <= 0 || p.Height <= 0 {
		return fmt.Errorf("invalid dimensions: %dx%d", p.Width, p.Height)
	}
	if p.VideoBitrate == "" {
		return fmt.Errorf("video bitrate is required")
	}
	if p.AudioBitrate == "" {
		return fmt.Errorf("audio bitrate is required")
	}
	if p.Profile == "" {
		p.Profile = "main"
	}
	if p.Level == "" {
		p.Level = "4.0"
	}
	if p.FrameRate <= 0 {
		p.FrameRate = 30.0
	}
	if p.GOPSeconds <= 0 {
		p.GOPSeconds = 2.0
	}
	return nil
}

// GetPresetByName returns a preset by name from a slice.
func GetPresetByName(presets []PresetConfig, name string) *PresetConfig {
	for i := range presets {
		if presets[i].Name == name {
			return &presets[i]
		}
	}
	return nil
}

// GetPresetByHeight returns the preset matching the given height.
func GetPresetByHeight(presets []PresetConfig, height int) *PresetConfig {
	for i := range presets {
		if presets[i].Height == height {
			return &presets[i]
		}
	}
	return nil
}

// ParseBitrate parses a bitrate string (e.g., "5000k", "2.5M") to bits per second.
func ParseBitrate(bitrate string) int64 {
	bitrate = strings.TrimSpace(strings.ToLower(bitrate))
	if bitrate == "" {
		return 0
	}

	multiplier := int64(1)
	if strings.HasSuffix(bitrate, "k") {
		multiplier = 1000
		bitrate = strings.TrimSuffix(bitrate, "k")
	} else if strings.HasSuffix(bitrate, "m") {
		multiplier = 1000000
		bitrate = strings.TrimSuffix(bitrate, "m")
	}

	val, err := strconv.ParseFloat(bitrate, 64)
	if err != nil {
		return 0
	}

	return int64(val * float64(multiplier))
}

// GetAVCCodecString returns the AVC codec string for a preset.
func GetAVCCodecString(profile, level string) string {
	profileIdc := "4d" // main
	switch strings.ToLower(profile) {
	case "baseline":
		profileIdc = "42"
	case "main":
		profileIdc = "4d"
	case "high":
		profileIdc = "64"
	}

	levelIdc := "28" // 4.0
	switch level {
	case "3.0":
		levelIdc = "1e"
	case "3.1":
		levelIdc = "1f"
	case "4.0":
		levelIdc = "28"
	case "4.1":
		levelIdc = "29"
	case "4.2":
		levelIdc = "2a"
	case "5.0":
		levelIdc = "32"
	case "5.1":
		levelIdc = "33"
	}

	return fmt.Sprintf("%s00%s", profileIdc, levelIdc)
}

// GOPFrames calculates the number of frames per GOP based on frame rate.
func (p *PresetConfig) GOPFrames() int {
	gop := p.GOPSeconds
	if gop <= 0 {
		gop = 2.0
	}
	fps := p.FrameRate
	if fps <= 0 {
		fps = 30.0
	}
	return int(gop * fps)
}

// Bandwidth returns the total bandwidth (video + audio) in bits per second.
func (p *PresetConfig) Bandwidth() int64 {
	return ParseBitrate(p.VideoBitrate) + ParseBitrate(p.AudioBitrate)
}

// PeakBandwidth returns the peak bandwidth (using maxrate if available).
func (p *PresetConfig) PeakBandwidth() int64 {
	if p.MaxRate != "" {
		return ParseBitrate(p.MaxRate) + ParseBitrate(p.AudioBitrate)
	}
	return int64(float64(p.Bandwidth()) * 1.1)
}

// CodecString returns the full codec string for HLS/DASH manifests.
func (p *PresetConfig) CodecString() string {
	return fmt.Sprintf("avc1.%s,mp4a.40.2", GetAVCCodecString(p.Profile, p.Level))
}

// VideoCodecString returns just the video codec string.
func (p *PresetConfig) VideoCodecString() string {
	return fmt.Sprintf("avc1.%s", GetAVCCodecString(p.Profile, p.Level))
}
