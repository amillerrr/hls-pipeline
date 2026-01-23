package config

import (
	"fmt"
	"strconv"
	"strings"
)

// PresetConfig defines a transcoding preset with encoding parameters.
type PresetConfig struct {
	// Name is the preset identifier (e.g., "1080p", "720p", "480p")
	Name string `json:"name"`

	// Video dimensions
	Width  int `json:"width"`
	Height int `json:"height"`

	// Video encoding
	VideoBitrate string  `json:"videoBitrate"` // e.g., "5000k"
	Profile      string  `json:"profile"`      // e.g., "high", "main", "baseline"
	Level        string  `json:"level"`        // e.g., "4.0", "4.1", "5.0"
	FrameRate    float64 `json:"frameRate"`

	// Audio encoding
	AudioBitrate string `json:"audioBitrate"` // e.g., "128k"
	AudioCodec   string `json:"audioCodec"`   // e.g., "aac"
	SampleRate   int    `json:"sampleRate"`   // e.g., 48000
	Channels     int    `json:"channels"`     // e.g., 2

	// Optional settings
	MaxBitrate string `json:"maxBitrate,omitempty"` // Max bitrate for VBV
	BufSize    string `json:"bufSize,omitempty"`    // VBV buffer size
}

// Bandwidth returns the total bandwidth for the preset in bits per second.
func (p PresetConfig) Bandwidth() int64 {
	videoBps := ParseBitrate(p.VideoBitrate)
	audioBps := ParseBitrate(p.AudioBitrate)
	return videoBps + audioBps
}

// PeakBandwidth returns the peak bandwidth (1.5x average for variable bitrate).
func (p PresetConfig) PeakBandwidth() int64 {
	return int64(float64(p.Bandwidth()) * 1.5)
}

// CodecString returns the HLS/DASH codec string for this preset.
func (p PresetConfig) CodecString() string {
	videoCodec := GetAVCCodecString(p.Profile, p.Level)
	audioCodec := "mp4a.40.2" // AAC-LC
	return fmt.Sprintf("%s,%s", videoCodec, audioCodec)
}

// DefaultPresets returns the standard set of transcoding presets.
func DefaultPresets() []PresetConfig {
	return []PresetConfig{
		{
			Name:         "1080p",
			Width:        1920,
			Height:       1080,
			VideoBitrate: "5000k",
			Profile:      "high",
			Level:        "4.0",
			FrameRate:    30.0,
			AudioBitrate: "192k",
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
		{
			Name:         "720p",
			Width:        1280,
			Height:       720,
			VideoBitrate: "2500k",
			Profile:      "high",
			Level:        "3.1",
			FrameRate:    30.0,
			AudioBitrate: "128k",
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
		{
			Name:         "480p",
			Width:        854,
			Height:       480,
			VideoBitrate: "1200k",
			Profile:      "main",
			Level:        "3.1",
			FrameRate:    30.0,
			AudioBitrate: "96k",
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
		{
			Name:         "360p",
			Width:        640,
			Height:       360,
			VideoBitrate: "600k",
			Profile:      "main",
			Level:        "3.0",
			FrameRate:    30.0,
			AudioBitrate: "64k",
			AudioCodec:   "aac",
			SampleRate:   44100,
			Channels:     2,
		},
	}
}

// LLHLSPresets returns presets optimized for low-latency streaming.
func LLHLSPresets() []PresetConfig {
	presets := DefaultPresets()

	// Adjust for LL-HLS - slightly lower bitrates for reduced latency
	for i := range presets {
		videoBps := ParseBitrate(presets[i].VideoBitrate)
		presets[i].VideoBitrate = fmt.Sprintf("%dk", int(float64(videoBps)*0.9/1000))
	}

	return presets
}

// LivePresets returns presets optimized for live streaming.
func LivePresets() []PresetConfig {
	return []PresetConfig{
		{
			Name:         "1080p",
			Width:        1920,
			Height:       1080,
			VideoBitrate: "4500k",
			Profile:      "high",
			Level:        "4.1",
			FrameRate:    30.0,
			AudioBitrate: "128k",
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
		{
			Name:         "720p",
			Width:        1280,
			Height:       720,
			VideoBitrate: "2000k",
			Profile:      "main",
			Level:        "3.1",
			FrameRate:    30.0,
			AudioBitrate: "128k",
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
		{
			Name:         "480p",
			Width:        854,
			Height:       480,
			VideoBitrate: "1000k",
			Profile:      "main",
			Level:        "3.1",
			FrameRate:    30.0,
			AudioBitrate: "96k",
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
	}
}

// MobilePresets returns presets optimized for mobile devices.
func MobilePresets() []PresetConfig {
	return []PresetConfig{
		{
			Name:         "720p",
			Width:        1280,
			Height:       720,
			VideoBitrate: "2000k",
			Profile:      "main",
			Level:        "3.1",
			FrameRate:    30.0,
			AudioBitrate: "128k",
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
		{
			Name:         "480p",
			Width:        854,
			Height:       480,
			VideoBitrate: "800k",
			Profile:      "main",
			Level:        "3.0",
			FrameRate:    30.0,
			AudioBitrate: "64k",
			AudioCodec:   "aac",
			SampleRate:   44100,
			Channels:     2,
		},
		{
			Name:         "360p",
			Width:        640,
			Height:       360,
			VideoBitrate: "400k",
			Profile:      "baseline",
			Level:        "3.0",
			FrameRate:    30.0,
			AudioBitrate: "48k",
			AudioCodec:   "aac",
			SampleRate:   44100,
			Channels:     2,
		},
	}
}

// GetPresetByName returns a preset by name from a preset list.
func GetPresetByName(presets []PresetConfig, name string) *PresetConfig {
	for i := range presets {
		if presets[i].Name == name {
			return &presets[i]
		}
	}
	return nil
}

// FilterPresets returns only the presets matching the given names.
func FilterPresets(presets []PresetConfig, names []string) []PresetConfig {
	if len(names) == 0 {
		return presets
	}

	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}

	var filtered []PresetConfig
	for _, p := range presets {
		if nameSet[p.Name] {
			filtered = append(filtered, p)
		}
	}

	return filtered
}

// ParseBitrate parses a bitrate string (e.g., "5000k", "2M") to bits per second.
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

	value, err := strconv.ParseInt(bitrate, 10, 64)
	if err != nil {
		return 0
	}

	return value * multiplier
}

// FormatBitrate formats bits per second to a human-readable string.
func FormatBitrate(bps int64) string {
	if bps >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(bps)/1000000)
	}
	if bps >= 1000 {
		return fmt.Sprintf("%dk", bps/1000)
	}
	return fmt.Sprintf("%d", bps)
}

// GetAVCCodecString returns the HLS/DASH codec string for an AVC profile and level.
func GetAVCCodecString(profile, level string) string {
	// Profile codes: baseline=42, main=4D, high=64
	profileCode := "4D" // main
	switch strings.ToLower(profile) {
	case "baseline":
		profileCode = "42"
	case "main":
		profileCode = "4D"
	case "high":
		profileCode = "64"
	}

	// Level codes: 3.0=1E, 3.1=1F, 4.0=28, 4.1=29, 5.0=32
	levelCode := "1F" // 3.1
	switch level {
	case "3.0":
		levelCode = "1E"
	case "3.1":
		levelCode = "1F"
	case "4.0":
		levelCode = "28"
	case "4.1":
		levelCode = "29"
	case "5.0":
		levelCode = "32"
	case "5.1":
		levelCode = "33"
	}

	return fmt.Sprintf("avc1.%s00%s", profileCode, levelCode)
}

// GetAACCodecString returns the HLS/DASH codec string for AAC.
func GetAACCodecString(channels int) string {
	if channels > 2 {
		return "mp4a.40.5" // HE-AAC for surround
	}
	return "mp4a.40.2" // AAC-LC for stereo
}

