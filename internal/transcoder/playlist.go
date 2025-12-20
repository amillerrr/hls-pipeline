package transcoder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GenerateMasterPlaylist creates the master HLS playlist file.
func GenerateMasterPlaylist(hlsDir string, presets []Preset) error {
	var builder strings.Builder
	builder.WriteString("#EXTM3U\n")
	builder.WriteString("#EXT-X-VERSION:3\n")

	for _, preset := range presets {
		builder.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n",
			preset.Bandwidth, preset.Width, preset.Height))
		builder.WriteString(fmt.Sprintf("%s/playlist.m3u8\n", preset.Name))
	}

	return os.WriteFile(filepath.Join(hlsDir, "master.m3u8"), []byte(builder.String()), 0644)
}

// CreateOutputDirectories creates the output directories for each quality level.
func CreateOutputDirectories(hlsDir string, presets []Preset) error {
	for _, preset := range presets {
		dirPath := filepath.Join(hlsDir, preset.Name)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			return fmt.Errorf("failed to create HLS subdir %s: %w", preset.Name, err)
		}
	}
	return nil
}
