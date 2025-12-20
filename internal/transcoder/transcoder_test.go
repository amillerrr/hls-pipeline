package transcoder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildFilterComplex(t *testing.T) {
	tests := []struct {
		name    string
		presets []Preset
		want    string
	}{
		{
			name:    "empty presets",
			presets: []Preset{},
			want:    "",
		},
		{
			name: "single preset",
			presets: []Preset{
				{"720p", 1280, 720, "2.5M", "2.75M", "5M", "128k", 2750000},
			},
			want: "[0:v]split=1[v1];[v1]scale=1280:720[v1out]",
		},
		{
			name: "multiple presets",
			presets: []Preset{
				{"1080p", 1920, 1080, "5M", "5.5M", "7.5M", "192k", 5500000},
				{"720p", 1280, 720, "2.5M", "2.75M", "5M", "128k", 2750000},
				{"480p", 854, 480, "1M", "1.1M", "2M", "96k", 1100000},
			},
			want: "[0:v]split=3[v1][v2][v3];[v1]scale=1920:1080[v1out];[v2]scale=1280:720[v2out];[v3]scale=854:480[v3out]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildFilterComplex(tt.presets)
			if got != tt.want {
				t.Errorf("BuildFilterComplex() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetPresetByHeight(t *testing.T) {
	presets := DefaultPresets

	tests := []struct {
		height   int
		wantName string
		wantNil  bool
	}{
		{1080, "1080p", false},
		{720, "720p", false},
		{480, "480p", false},
		{360, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.wantName, func(t *testing.T) {
			got := GetPresetByHeight(presets, tt.height)
			if tt.wantNil {
				if got != nil {
					t.Errorf("GetPresetByHeight(%d) = %v, want nil", tt.height, got)
				}
			} else {
				if got == nil {
					t.Errorf("GetPresetByHeight(%d) = nil, want %s", tt.height, tt.wantName)
				} else if got.Name != tt.wantName {
					t.Errorf("GetPresetByHeight(%d).Name = %s, want %s", tt.height, got.Name, tt.wantName)
				}
			}
		})
	}
}

func TestGetPresetByName(t *testing.T) {
	presets := DefaultPresets

	tests := []struct {
		name       string
		wantHeight int
		wantNil    bool
	}{
		{"1080p", 1080, false},
		{"720p", 720, false},
		{"480p", 480, false},
		{"360p", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetPresetByName(presets, tt.name)
			if tt.wantNil {
				if got != nil {
					t.Errorf("GetPresetByName(%s) = %v, want nil", tt.name, got)
				}
			} else {
				if got == nil {
					t.Errorf("GetPresetByName(%s) = nil, want height %d", tt.name, tt.wantHeight)
				} else if got.Height != tt.wantHeight {
					t.Errorf("GetPresetByName(%s).Height = %d, want %d", tt.name, got.Height, tt.wantHeight)
				}
			}
		})
	}
}

func TestToModelPresets(t *testing.T) {
	presets := []Preset{
		{"1080p", 1920, 1080, "5M", "5.5M", "7.5M", "192k", 5500000},
		{"720p", 1280, 720, "2.5M", "2.75M", "5M", "128k", 2750000},
	}

	result := ToModelPresets(presets)

	if len(result) != 2 {
		t.Fatalf("ToModelPresets() len = %d, want 2", len(result))
	}

	if result[0].Name != "1080p" {
		t.Errorf("result[0].Name = %s, want 1080p", result[0].Name)
	}
	if result[0].Bitrate != 5500000 {
		t.Errorf("result[0].Bitrate = %d, want 5500000", result[0].Bitrate)
	}
	if result[1].Width != 1280 {
		t.Errorf("result[1].Width = %d, want 1280", result[1].Width)
	}
}

func TestGenerateMasterPlaylist(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "hls-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	presets := []Preset{
		{"1080p", 1920, 1080, "5M", "5.5M", "7.5M", "192k", 5500000},
		{"720p", 1280, 720, "2.5M", "2.75M", "5M", "128k", 2750000},
	}

	err = GenerateMasterPlaylist(tmpDir, presets)
	if err != nil {
		t.Fatalf("GenerateMasterPlaylist() error = %v", err)
	}

	// Read the generated file
	content, err := os.ReadFile(filepath.Join(tmpDir, "master.m3u8"))
	if err != nil {
		t.Fatalf("Failed to read master.m3u8: %v", err)
	}

	contentStr := string(content)

	// Check required content
	if !strings.Contains(contentStr, "#EXTM3U") {
		t.Error("master.m3u8 missing #EXTM3U header")
	}
	if !strings.Contains(contentStr, "BANDWIDTH=5500000") {
		t.Error("master.m3u8 missing 1080p bandwidth")
	}
	if !strings.Contains(contentStr, "RESOLUTION=1920x1080") {
		t.Error("master.m3u8 missing 1080p resolution")
	}
	if !strings.Contains(contentStr, "1080p/playlist.m3u8") {
		t.Error("master.m3u8 missing 1080p playlist reference")
	}
	if !strings.Contains(contentStr, "720p/playlist.m3u8") {
		t.Error("master.m3u8 missing 720p playlist reference")
	}
}

func TestCreateOutputDirectories(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "hls-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	hlsDir := filepath.Join(tmpDir, "output")

	err = CreateOutputDirectories(hlsDir, DefaultPresets)
	if err != nil {
		t.Fatalf("CreateOutputDirectories() error = %v", err)
	}

	// Check directories were created
	for _, preset := range DefaultPresets {
		dirPath := filepath.Join(hlsDir, preset.Name)
		info, err := os.Stat(dirPath)
		if err != nil {
			t.Errorf("Directory %s not created: %v", preset.Name, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", preset.Name)
		}
	}
}
