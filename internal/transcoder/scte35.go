// internal/transcoder/scte35.go
package transcoder

import (
	"encoding/base64"
	"encoding/binary"
)

// SCTE35Marker represents an ad break opportunity
type SCTE35Marker struct {
	PTS           uint64
	Duration      float64
	SegmentNum    int
	AvailNum      int
	AvailExpected int
}

// GenerateSCTE35Cue creates a SCTE-35 splice_insert command
func GenerateSCTE35Cue(marker SCTE35Marker) string {
	// Simplified SCTE-35 splice_insert
	// In production, use a proper SCTE-35 library

	data := make([]byte, 20)

	// Table ID (0xFC for SCTE-35)
	data[0] = 0xFC

	// Section syntax indicator + private indicator + reserved + section length
	binary.BigEndian.PutUint16(data[1:3], 0x3012)

	// Protocol version
	data[3] = 0x00

	// Encrypted packet + encryption algorithm + pts_adjustment
	binary.BigEndian.PutUint64(data[4:12], marker.PTS)

	// CW index + tier + splice command length
	binary.BigEndian.PutUint16(data[12:14], 0x0FFF)

	// Splice command type (0x05 = splice_insert)
	data[14] = 0x05

	return base64.StdEncoding.EncodeToString(data)
}

// InjectAdMarkers modifies the HLS playlist to include ad markers
func InjectAdMarkers(playlist string, markers []SCTE35Marker) string {
	var result strings.Builder
	lines := strings.Split(playlist, "\n")

	markerIndex := 0
	segmentNum := 0

	for _, line := range lines {
		if strings.HasPrefix(line, "#EXTINF:") {
			// Check if we should insert an ad marker before this segment
			if markerIndex < len(markers) && segmentNum == markers[markerIndex].SegmentNum {
				m := markers[markerIndex]
				cue := GenerateSCTE35Cue(m)

				result.WriteString(fmt.Sprintf("#EXT-X-CUE-OUT:DURATION=%.1f\n", m.Duration))
				result.WriteString(fmt.Sprintf("#EXT-X-SCTE35:CUE=\"%s\"\n", cue))
				markerIndex++
			}
			segmentNum++
		}
		result.WriteString(line + "\n")
	}

	return result.String()
}
