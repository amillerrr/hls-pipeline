package ssai

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// SCTE35MessageType defines the type of SCTE-35 message.
type SCTE35MessageType int

const (
	// SpliceNull is a null splice command (used for heartbeat).
	SpliceNull SCTE35MessageType = 0x00
	// SpliceSchedule is a scheduled splice.
	SpliceSchedule SCTE35MessageType = 0x04
	// SpliceInsert is an immediate or scheduled splice insert.
	SpliceInsert SCTE35MessageType = 0x05
	// TimeSignal is a time signal for ad insertion.
	TimeSignal SCTE35MessageType = 0x06
	// BandwidthReservation reserves bandwidth.
	BandwidthReservation SCTE35MessageType = 0x07
	// PrivateCommand is a private/custom command.
	PrivateCommand SCTE35MessageType = 0xFF
)

// SCTE35Marker represents an SCTE-35 ad marker.
type SCTE35Marker struct {
	PTS            uint64            `json:"pts"`
	Duration       float64           `json:"duration"`
	SegmentNum     int               `json:"segmentNum"`
	SpliceEventID  uint32            `json:"spliceEventId"`
	Type           string            `json:"type"` // "OUT" or "IN"
	AvailNum       int               `json:"availNum,omitempty"`
	AvailsExpected int               `json:"availsExpected,omitempty"`
	UPID           string            `json:"upid,omitempty"`
	SegmentationTypeID uint8         `json:"segmentationTypeId,omitempty"`
}

// SCTE35Command represents a parsed SCTE-35 command.
type SCTE35Command struct {
	TableID              uint8              `json:"tableId"`
	SectionLength        uint16             `json:"sectionLength"`
	ProtocolVersion      uint8              `json:"protocolVersion"`
	EncryptedPacket      bool               `json:"encryptedPacket"`
	PTSAdjustment        uint64             `json:"ptsAdjustment"`
	Tier                 uint16             `json:"tier"`
	CommandType          SCTE35MessageType  `json:"commandType"`
	CommandLength        uint16             `json:"commandLength"`
	SpliceTime           *SpliceTime        `json:"spliceTime,omitempty"`
	BreakDuration        *BreakDuration     `json:"breakDuration,omitempty"`
	SpliceEventID        uint32             `json:"spliceEventId,omitempty"`
	SpliceEventCancelIndicator bool         `json:"spliceEventCancelIndicator,omitempty"`
	OutOfNetworkIndicator bool              `json:"outOfNetworkIndicator,omitempty"`
	ProgramSpliceFlag    bool               `json:"programSpliceFlag,omitempty"`
	DurationFlag         bool               `json:"durationFlag,omitempty"`
	SpliceImmediateFlag  bool               `json:"spliceImmediateFlag,omitempty"`
	Descriptors          []SpliceDescriptor `json:"descriptors,omitempty"`
}

// SpliceTime represents a splice time.
type SpliceTime struct {
	TimeSpecifiedFlag bool   `json:"timeSpecifiedFlag"`
	PTSTime           uint64 `json:"ptsTime,omitempty"`
}

// BreakDuration represents the duration of an ad break.
type BreakDuration struct {
	AutoReturn bool   `json:"autoReturn"`
	Duration   uint64 `json:"duration"` // In 90kHz ticks
}

// SpliceDescriptor represents an SCTE-35 splice descriptor.
type SpliceDescriptor struct {
	Tag         uint8  `json:"tag"`
	Length      uint8  `json:"length"`
	Identifier  uint32 `json:"identifier"`
	PrivateData []byte `json:"privateData,omitempty"`
	
	// Segmentation descriptor specific fields
	SegmentationEventID uint32 `json:"segmentationEventId,omitempty"`
	SegmentationTypeID  uint8  `json:"segmentationTypeId,omitempty"`
	SegmentNum          uint8  `json:"segmentNum,omitempty"`
	SegmentsExpected    uint8  `json:"segmentsExpected,omitempty"`
	UPID                []byte `json:"upid,omitempty"`
}

// GenerateSCTE35Payload generates a binary SCTE-35 payload.
func GenerateSCTE35Payload(marker *SCTE35Marker) ([]byte, error) {
	var data []byte

	// Table ID (0xFC for splice_info_section)
	data = append(data, 0xFC)

	// Calculate section length later
	sectionStart := len(data)
	data = append(data, 0x00, 0x00) // Placeholder

	// Protocol version
	data = append(data, 0x00)

	// Encrypted packet (1 bit) + encryption algorithm (6 bits) + pts_adjustment (33 bits)
	ptsAdj := marker.PTS
	data = append(data,
		byte(ptsAdj>>32)&0x01,
		byte(ptsAdj>>24),
		byte(ptsAdj>>16),
		byte(ptsAdj>>8),
		byte(ptsAdj),
	)

	// CW index
	data = append(data, 0x00)

	// Tier (12 bits) = 0xFFF (all tiers)
	data = append(data, 0x0F, 0xFF)

	// Command length (12 bits) - will be filled later
	cmdLengthPos := len(data)
	data = append(data, 0x00, 0x00)

	// Command type
	cmdType := SpliceInsert
	if marker.Type == "IN" {
		// For splice-in, we still use SpliceInsert but with out_of_network_indicator = 0
		cmdType = SpliceInsert
	}
	data = append(data, byte(cmdType))

	// Splice Insert command
	cmdStart := len(data)

	// Splice event ID (32 bits)
	eventID := marker.SpliceEventID
	if eventID == 0 {
		eventID = uint32(marker.SegmentNum)
	}
	data = append(data,
		byte(eventID>>24),
		byte(eventID>>16),
		byte(eventID>>8),
		byte(eventID),
	)

	// Splice event cancel indicator (1 bit) + reserved (7 bits)
	data = append(data, 0x00)

	// out_of_network_indicator (1 bit) + program_splice_flag (1 bit) +
	// duration_flag (1 bit) + splice_immediate_flag (1 bit) + reserved (4 bits)
	flags := byte(0x00)
	if marker.Type == "OUT" {
		flags |= 0x80 // out_of_network_indicator
	}
	flags |= 0x40 // program_splice_flag
	if marker.Duration > 0 {
		flags |= 0x20 // duration_flag
	}
	flags |= 0x10 // splice_immediate_flag
	data = append(data, flags)

	// Break duration (if duration_flag is set)
	if marker.Duration > 0 {
		durationTicks := uint64(marker.Duration * 90000) // Convert to 90kHz ticks
		
		// auto_return (1 bit) + reserved (6 bits) + duration (33 bits)
		data = append(data,
			0x80|byte(durationTicks>>32)&0x01, // auto_return = 1
			byte(durationTicks>>24),
			byte(durationTicks>>16),
			byte(durationTicks>>8),
			byte(durationTicks),
		)
	}

	// Unique program ID (16 bits)
	data = append(data, 0x00, 0x00)

	// Avail num (8 bits)
	data = append(data, byte(marker.AvailNum))

	// Avails expected (8 bits)
	data = append(data, byte(marker.AvailsExpected))

	// Update command length
	cmdLength := len(data) - cmdStart
	data[cmdLengthPos] = byte(cmdLength >> 8)
	data[cmdLengthPos+1] = byte(cmdLength)

	// Descriptor loop length (16 bits) - no descriptors for now
	data = append(data, 0x00, 0x00)

	// Update section length
	sectionLength := len(data) - sectionStart - 2 + 4 // +4 for CRC
	data[sectionStart] = 0x80 | byte(sectionLength>>8)&0x0F // section_syntax_indicator + private + reserved + length
	data[sectionStart+1] = byte(sectionLength)

	// CRC-32 (MPEG-2)
	crc := calculateCRC32MPEG2(data)
	data = append(data,
		byte(crc>>24),
		byte(crc>>16),
		byte(crc>>8),
		byte(crc),
	)

	return data, nil
}

// calculateCRC32MPEG2 calculates the CRC-32 checksum for MPEG-2.
func calculateCRC32MPEG2(data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	
	for _, b := range data {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	
	return crc
}

// InsertCueOutTag generates an EXT-X-CUE-OUT tag.
func InsertCueOutTag(duration float64) string {
	return fmt.Sprintf("#EXT-X-CUE-OUT:%.1f", duration)
}

// InsertCueInTag generates an EXT-X-CUE-IN tag.
func InsertCueInTag() string {
	return "#EXT-X-CUE-IN"
}

// InsertDateRangeTag generates an EXT-X-DATERANGE tag with SCTE-35 data.
func InsertDateRangeTag(marker *SCTE35Marker, startDate time.Time) (string, error) {
	payload, err := GenerateSCTE35Payload(marker)
	if err != nil {
		return "", err
	}

	encodedPayload := base64.StdEncoding.EncodeToString(payload)

	var tag strings.Builder
	tag.WriteString("#EXT-X-DATERANGE:")
	tag.WriteString(fmt.Sprintf(`ID="%d",`, marker.SpliceEventID))
	tag.WriteString(fmt.Sprintf(`START-DATE="%s",`, startDate.Format(time.RFC3339Nano)))
	
	if marker.Duration > 0 {
		tag.WriteString(fmt.Sprintf(`DURATION=%.3f,`, marker.Duration))
	}
	
	tag.WriteString(fmt.Sprintf(`SCTE35-CMD=%s`, encodedPayload))

	return tag.String(), nil
}

// InsertCuePoints inserts SCTE-35 cue points into an HLS playlist.
func InsertCuePoints(playlist string, markers []SCTE35Marker) string {
	lines := strings.Split(playlist, "\n")
	var result strings.Builder

	markerIdx := 0
	segmentNum := 0

	for _, line := range lines {
		// Check if we need to insert a cue before this line
		if strings.HasPrefix(line, "#EXTINF:") {
			for markerIdx < len(markers) && markers[markerIdx].SegmentNum == segmentNum {
				marker := markers[markerIdx]
				
				if marker.Type == "OUT" {
					result.WriteString(InsertCueOutTag(marker.Duration) + "\n")
				} else if marker.Type == "IN" {
					result.WriteString(InsertCueInTag() + "\n")
				}
				
				markerIdx++
			}
			segmentNum++
		}

		result.WriteString(line + "\n")
	}

	return result.String()
}

// ParseSCTE35Base64 parses a base64-encoded SCTE-35 payload.
func ParseSCTE35Base64(encoded string) (*SCTE35Command, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode SCTE-35 payload: %w", err)
	}

	return ParseSCTE35(data)
}

// ParseSCTE35 parses a binary SCTE-35 payload.
func ParseSCTE35(data []byte) (*SCTE35Command, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("SCTE-35 payload too short")
	}

	cmd := &SCTE35Command{}
	offset := 0

	// Table ID
	cmd.TableID = data[offset]
	offset++

	if cmd.TableID != 0xFC {
		return nil, fmt.Errorf("invalid SCTE-35 table ID: 0x%02X", cmd.TableID)
	}

	// Section length
	cmd.SectionLength = binary.BigEndian.Uint16(data[offset:]) & 0x0FFF
	offset += 2

	// Protocol version
	cmd.ProtocolVersion = data[offset]
	offset++

	// Encrypted packet and PTS adjustment
	cmd.EncryptedPacket = (data[offset] & 0x80) != 0
	cmd.PTSAdjustment = uint64(data[offset]&0x01)<<32 |
		uint64(data[offset+1])<<24 |
		uint64(data[offset+2])<<16 |
		uint64(data[offset+3])<<8 |
		uint64(data[offset+4])
	offset += 5

	// CW index
	offset++

	// Tier
	cmd.Tier = binary.BigEndian.Uint16(data[offset:]) >> 4
	offset += 2

	// Command length
	cmd.CommandLength = binary.BigEndian.Uint16(data[offset:]) & 0x0FFF
	offset += 2

	// Command type
	cmd.CommandType = SCTE35MessageType(data[offset])
	offset++

	// Parse command based on type
	switch cmd.CommandType {
	case SpliceInsert:
		if err := parseSpliceInsert(cmd, data[offset:]); err != nil {
			return nil, err
		}
	case TimeSignal:
		if err := parseTimeSignal(cmd, data[offset:]); err != nil {
			return nil, err
		}
	}

	return cmd, nil
}

// parseSpliceInsert parses a splice_insert command.
func parseSpliceInsert(cmd *SCTE35Command, data []byte) error {
	if len(data) < 5 {
		return fmt.Errorf("splice_insert data too short")
	}

	offset := 0

	// Splice event ID
	cmd.SpliceEventID = binary.BigEndian.Uint32(data[offset:])
	offset += 4

	// Cancel indicator
	cmd.SpliceEventCancelIndicator = (data[offset] & 0x80) != 0
	offset++

	if !cmd.SpliceEventCancelIndicator {
		if len(data) < offset+1 {
			return fmt.Errorf("splice_insert data too short")
		}

		flags := data[offset]
		offset++

		cmd.OutOfNetworkIndicator = (flags & 0x80) != 0
		cmd.ProgramSpliceFlag = (flags & 0x40) != 0
		cmd.DurationFlag = (flags & 0x20) != 0
		cmd.SpliceImmediateFlag = (flags & 0x10) != 0

		// Parse duration if present
		if cmd.DurationFlag && len(data) >= offset+5 {
			autoReturn := (data[offset] & 0x80) != 0
			duration := uint64(data[offset]&0x01)<<32 |
				uint64(data[offset+1])<<24 |
				uint64(data[offset+2])<<16 |
				uint64(data[offset+3])<<8 |
				uint64(data[offset+4])

			cmd.BreakDuration = &BreakDuration{
				AutoReturn: autoReturn,
				Duration:   duration,
			}
		}
	}

	return nil
}

// parseTimeSignal parses a time_signal command.
func parseTimeSignal(cmd *SCTE35Command, data []byte) error {
	if len(data) < 1 {
		return fmt.Errorf("time_signal data too short")
	}

	timeSpecified := (data[0] & 0x80) != 0
	cmd.SpliceTime = &SpliceTime{
		TimeSpecifiedFlag: timeSpecified,
	}

	if timeSpecified && len(data) >= 5 {
		cmd.SpliceTime.PTSTime = uint64(data[0]&0x01)<<32 |
			uint64(data[1])<<24 |
			uint64(data[2])<<16 |
			uint64(data[3])<<8 |
			uint64(data[4])
	}

	return nil
}

// GetBreakDurationSeconds returns the break duration in seconds.
func (b *BreakDuration) GetBreakDurationSeconds() float64 {
	return float64(b.Duration) / 90000.0
}

