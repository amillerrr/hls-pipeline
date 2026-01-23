package transcoder

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// SCTE35 table IDs
const (
	SCTE35TableID        = 0xFC
	SCTE35SpliceInfoType = 0x00
)

// SCTE35 splice command types
const (
	SpliceCommandNull         = 0x00
	SpliceCommandSchedule     = 0x04
	SpliceCommandInsert       = 0x05
	SpliceCommandTimeSignal   = 0x06
	SpliceCommandBandwidth    = 0x07
	SpliceCommandPrivate      = 0xFF
)

// SCTE35Marker represents a SCTE-35 marker in the stream.
type SCTE35Marker struct {
	// PTS is the presentation timestamp where the marker occurs.
	PTS time.Duration `json:"pts"`

	// Type is the splice command type.
	Type SCTE35Type `json:"type"`

	// Duration is the duration of the ad break (if specified).
	Duration time.Duration `json:"duration,omitempty"`

	// EventID is the unique identifier for this splice event.
	EventID uint32 `json:"eventId"`

	// AvailNum is the avail number within the break.
	AvailNum uint8 `json:"availNum"`

	// AvailsExpected is the total number of avails expected.
	AvailsExpected uint8 `json:"availsExpected"`

	// OutOfNetworkIndicator indicates if this is an out-of-network splice.
	OutOfNetworkIndicator bool `json:"outOfNetworkIndicator"`

	// ImmediateSplice indicates if the splice should happen immediately.
	ImmediateSplice bool `json:"immediateSplice"`

	// RawData contains the original SCTE-35 binary data.
	RawData []byte `json:"rawData,omitempty"`

	// Base64 is the base64 encoded SCTE-35 message.
	Base64 string `json:"base64"`

	// SegmentationDescriptors contains any segmentation descriptors.
	SegmentationDescriptors []SegmentationDescriptor `json:"segmentationDescriptors,omitempty"`
}

// SCTE35Type represents the type of SCTE-35 marker.
type SCTE35Type string

const (
	SCTE35TypeSpliceInsert   SCTE35Type = "splice_insert"
	SCTE35TypeTimeSignal     SCTE35Type = "time_signal"
	SCTE35TypeSpliceSchedule SCTE35Type = "splice_schedule"
	SCTE35TypePrivate        SCTE35Type = "private"
	SCTE35TypeUnknown        SCTE35Type = "unknown"
)

// SegmentationDescriptor represents a SCTE-35 segmentation descriptor.
type SegmentationDescriptor struct {
	// SegmentationEventID is the unique ID for this segmentation event.
	SegmentationEventID uint32 `json:"segmentationEventId"`

	// SegmentationTypeID identifies the type of segmentation.
	SegmentationTypeID uint8 `json:"segmentationTypeId"`

	// SegmentationTypeName is the human-readable name of the type.
	SegmentationTypeName string `json:"segmentationTypeName"`

	// SegmentNum is the segment number within a multi-segment event.
	SegmentNum uint8 `json:"segmentNum"`

	// SegmentsExpected is the total number of segments expected.
	SegmentsExpected uint8 `json:"segmentsExpected"`

	// Duration is the duration of this segment.
	Duration time.Duration `json:"duration,omitempty"`

	// UPID is the unique program identifier.
	UPID string `json:"upid,omitempty"`

	// UPIDType is the type of UPID.
	UPIDType uint8 `json:"upidType"`
}

// Common segmentation type IDs
const (
	SegTypeNotIndicated                = 0x00
	SegTypeContentIdentification       = 0x01
	SegTypeProgramStart                = 0x10
	SegTypeProgramEnd                  = 0x11
	SegTypeProgramEarlyTermination     = 0x12
	SegTypeProgramBreakaway            = 0x13
	SegTypeProgramResumption           = 0x14
	SegTypeProgramRunoverPlanned       = 0x15
	SegTypeProgramRunoverUnplanned     = 0x16
	SegTypeProgramOverlapStart         = 0x17
	SegTypeProgramBlackoutOverride     = 0x18
	SegTypeChapterStart                = 0x20
	SegTypeChapterEnd                  = 0x21
	SegTypeBreakStart                  = 0x22
	SegTypeBreakEnd                    = 0x23
	SegTypeProviderAdStart             = 0x30
	SegTypeProviderAdEnd               = 0x31
	SegTypeDistributorAdStart          = 0x32
	SegTypeDistributorAdEnd            = 0x33
	SegTypeProviderPOStart             = 0x34
	SegTypeProviderPOEnd               = 0x35
	SegTypeDistributorPOStart          = 0x36
	SegTypeDistributorPOEnd            = 0x37
	SegTypeUnscheduledEventStart       = 0x40
	SegTypeUnscheduledEventEnd         = 0x41
	SegTypeNetworkStart                = 0x50
	SegTypeNetworkEnd                  = 0x51
)

// segmentationTypeNames maps type IDs to human-readable names.
var segmentationTypeNames = map[uint8]string{
	SegTypeNotIndicated:            "Not Indicated",
	SegTypeContentIdentification:   "Content Identification",
	SegTypeProgramStart:            "Program Start",
	SegTypeProgramEnd:              "Program End",
	SegTypeProgramEarlyTermination: "Program Early Termination",
	SegTypeProgramBreakaway:        "Program Breakaway",
	SegTypeProgramResumption:       "Program Resumption",
	SegTypeChapterStart:            "Chapter Start",
	SegTypeChapterEnd:              "Chapter End",
	SegTypeBreakStart:              "Break Start",
	SegTypeBreakEnd:                "Break End",
	SegTypeProviderAdStart:         "Provider Advertisement Start",
	SegTypeProviderAdEnd:           "Provider Advertisement End",
	SegTypeDistributorAdStart:      "Distributor Advertisement Start",
	SegTypeDistributorAdEnd:        "Distributor Advertisement End",
	SegTypeProviderPOStart:         "Provider Placement Opportunity Start",
	SegTypeProviderPOEnd:           "Provider Placement Opportunity End",
	SegTypeDistributorPOStart:      "Distributor Placement Opportunity Start",
	SegTypeDistributorPOEnd:        "Distributor Placement Opportunity End",
	SegTypeUnscheduledEventStart:   "Unscheduled Event Start",
	SegTypeUnscheduledEventEnd:     "Unscheduled Event End",
	SegTypeNetworkStart:            "Network Start",
	SegTypeNetworkEnd:              "Network End",
}

// SCTE35Parser parses SCTE-35 markers from various formats.
type SCTE35Parser struct {
	// StrictMode enables strict parsing (fail on invalid data).
	StrictMode bool
}

// NewSCTE35Parser creates a new SCTE-35 parser.
func NewSCTE35Parser() *SCTE35Parser {
	return &SCTE35Parser{
		StrictMode: false,
	}
}

// ParseBase64 parses a base64-encoded SCTE-35 message.
func (p *SCTE35Parser) ParseBase64(ctx context.Context, encoded string) (*SCTE35Marker, error) {
	ctx, span := tracer.Start(ctx, "scte35-parse-base64")
	defer span.End()

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	marker, err := p.ParseBinary(ctx, data)
	if err != nil {
		return nil, err
	}
	marker.Base64 = encoded

	return marker, nil
}

// ParseHex parses a hex-encoded SCTE-35 message.
func (p *SCTE35Parser) ParseHex(ctx context.Context, hexStr string) (*SCTE35Marker, error) {
	ctx, span := tracer.Start(ctx, "scte35-parse-hex")
	defer span.End()

	// Remove common prefixes
	hexStr = strings.TrimPrefix(hexStr, "0x")
	hexStr = strings.TrimPrefix(hexStr, "0X")

	data, err := hex.DecodeString(hexStr)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to decode hex: %w", err)
	}

	return p.ParseBinary(ctx, data)
}

// ParseBinary parses a binary SCTE-35 message.
func (p *SCTE35Parser) ParseBinary(ctx context.Context, data []byte) (*SCTE35Marker, error) {
	_, span := tracer.Start(ctx, "scte35-parse-binary",
		trace.WithAttributes(
			attribute.Int("data.length", len(data)),
		))
	defer span.End()

	if len(data) < 3 {
		return nil, fmt.Errorf("SCTE-35 message too short: %d bytes", len(data))
	}

	marker := &SCTE35Marker{
		RawData: data,
		Base64:  base64.StdEncoding.EncodeToString(data),
		Type:    SCTE35TypeUnknown,
	}

	// Check table ID
	tableID := data[0]
	if tableID != SCTE35TableID {
		if p.StrictMode {
			return nil, fmt.Errorf("invalid table ID: 0x%02X (expected 0xFC)", tableID)
		}
	}

	// Parse section syntax indicator and section length
	if len(data) < 14 {
		return nil, fmt.Errorf("SCTE-35 message too short for header")
	}

	// Skip to splice command type (byte 13 in typical message)
	offset := 13
	if offset >= len(data) {
		return marker, nil
	}

	spliceCommandType := data[offset]
	span.SetAttributes(attribute.Int("splice_command_type", int(spliceCommandType)))

	switch spliceCommandType {
	case SpliceCommandInsert:
		marker.Type = SCTE35TypeSpliceInsert
		p.parseSpliceInsert(marker, data, offset+1)

	case SpliceCommandTimeSignal:
		marker.Type = SCTE35TypeTimeSignal
		p.parseTimeSignal(marker, data, offset+1)

	case SpliceCommandSchedule:
		marker.Type = SCTE35TypeSpliceSchedule

	case SpliceCommandPrivate:
		marker.Type = SCTE35TypePrivate

	default:
		marker.Type = SCTE35TypeUnknown
	}

	return marker, nil
}

// parseSpliceInsert parses a splice_insert command.
func (p *SCTE35Parser) parseSpliceInsert(marker *SCTE35Marker, data []byte, offset int) {
	if offset+4 > len(data) {
		return
	}

	// Splice event ID (32 bits)
	marker.EventID = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4

	if offset >= len(data) {
		return
	}

	// Flags
	flags := data[offset]
	marker.OutOfNetworkIndicator = (flags & 0x80) != 0
	marker.ImmediateSplice = (flags & 0x10) != 0
	offset++

	// If not immediate, parse PTS
	if !marker.ImmediateSplice && offset+5 <= len(data) {
		// Skip program_splice_flag and duration_flag bits
		// Parse PTS if present
		if (flags & 0x40) != 0 { // time_specified_flag
			pts := p.parsePTS(data[offset : offset+5])
			marker.PTS = time.Duration(pts) * time.Second / 90000
			offset += 5
		}
	}

	// Parse duration if present
	if (flags & 0x20) != 0 && offset+5 <= len(data) { // duration_flag
		duration := p.parsePTS(data[offset : offset+5])
		marker.Duration = time.Duration(duration) * time.Second / 90000
	}

	// Parse avail info if present
	if offset+2 <= len(data) {
		marker.AvailNum = data[offset]
		marker.AvailsExpected = data[offset+1]
	}
}

// parseTimeSignal parses a time_signal command.
func (p *SCTE35Parser) parseTimeSignal(marker *SCTE35Marker, data []byte, offset int) {
	if offset+5 > len(data) {
		return
	}

	// Parse splice time
	if (data[offset] & 0x80) != 0 { // time_specified_flag
		pts := p.parsePTS(data[offset : offset+5])
		marker.PTS = time.Duration(pts) * time.Second / 90000
	}
}

// parsePTS parses a 33-bit PTS value from 5 bytes.
func (p *SCTE35Parser) parsePTS(data []byte) int64 {
	if len(data) < 5 {
		return 0
	}

	// PTS is 33 bits spread across 5 bytes
	pts := int64(data[0]&0x0E) << 29
	pts |= int64(data[1]) << 22
	pts |= int64(data[2]&0xFE) << 14
	pts |= int64(data[3]) << 7
	pts |= int64(data[4]) >> 1

	return pts
}

// ExtractMarkersFromHLS extracts SCTE-35 markers from an HLS playlist.
func (p *SCTE35Parser) ExtractMarkersFromHLS(ctx context.Context, playlist string) ([]*SCTE35Marker, error) {
	ctx, span := tracer.Start(ctx, "scte35-extract-hls")
	defer span.End()

	var markers []*SCTE35Marker

	// Patterns for SCTE-35 in HLS
	daterangeRe := regexp.MustCompile(`#EXT-X-DATERANGE:.*SCTE35-CMD=([A-Za-z0-9+/=]+)`)
	cueRe := regexp.MustCompile(`#EXT-X-CUE-OUT:DURATION=([\d.]+)`)
	cueInRe := regexp.MustCompile(`#EXT-X-CUE-IN`)
	oatclsRe := regexp.MustCompile(`#EXT-OATCLS-SCTE35:([A-Za-z0-9+/=]+)`)

	lines := strings.Split(playlist, "\n")
	var currentPTS time.Duration

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Parse EXTINF for timing
		if strings.HasPrefix(line, "#EXTINF:") {
			parts := strings.Split(strings.TrimPrefix(line, "#EXTINF:"), ",")
			if len(parts) > 0 {
				duration, _ := strconv.ParseFloat(parts[0], 64)
				currentPTS += time.Duration(duration * float64(time.Second))
			}
			continue
		}

		// EXT-X-DATERANGE with SCTE35-CMD
		if matches := daterangeRe.FindStringSubmatch(line); len(matches) > 1 {
			marker, err := p.ParseBase64(ctx, matches[1])
			if err == nil {
				marker.PTS = currentPTS
				markers = append(markers, marker)
			}
			continue
		}

		// EXT-X-CUE-OUT
		if matches := cueRe.FindStringSubmatch(line); len(matches) > 1 {
			duration, _ := strconv.ParseFloat(matches[1], 64)
			marker := &SCTE35Marker{
				PTS:                   currentPTS,
				Type:                  SCTE35TypeSpliceInsert,
				Duration:              time.Duration(duration * float64(time.Second)),
				OutOfNetworkIndicator: true,
			}
			markers = append(markers, marker)
			continue
		}

		// EXT-X-CUE-IN
		if cueInRe.MatchString(line) {
			marker := &SCTE35Marker{
				PTS:                   currentPTS,
				Type:                  SCTE35TypeSpliceInsert,
				OutOfNetworkIndicator: false,
			}
			markers = append(markers, marker)
			continue
		}

		// EXT-OATCLS-SCTE35 (legacy format)
		if matches := oatclsRe.FindStringSubmatch(line); len(matches) > 1 {
			marker, err := p.ParseBase64(ctx, matches[1])
			if err == nil {
				marker.PTS = currentPTS
				markers = append(markers, marker)
			}
			continue
		}
	}

	span.SetAttributes(attribute.Int("markers.count", len(markers)))

	return markers, nil
}

// IsAdStart returns true if this marker indicates an ad break start.
func (m *SCTE35Marker) IsAdStart() bool {
	if m.Type == SCTE35TypeSpliceInsert && m.OutOfNetworkIndicator {
		return true
	}

	for _, desc := range m.SegmentationDescriptors {
		switch desc.SegmentationTypeID {
		case SegTypeProviderAdStart, SegTypeDistributorAdStart,
			SegTypeProviderPOStart, SegTypeDistributorPOStart:
			return true
		}
	}

	return false
}

// IsAdEnd returns true if this marker indicates an ad break end.
func (m *SCTE35Marker) IsAdEnd() bool {
	if m.Type == SCTE35TypeSpliceInsert && !m.OutOfNetworkIndicator {
		return true
	}

	for _, desc := range m.SegmentationDescriptors {
		switch desc.SegmentationTypeID {
		case SegTypeProviderAdEnd, SegTypeDistributorAdEnd,
			SegTypeProviderPOEnd, SegTypeDistributorPOEnd:
			return true
		}
	}

	return false
}

// GetSegmentationTypeName returns the human-readable name for a segmentation type.
func GetSegmentationTypeName(typeID uint8) string {
	if name, ok := segmentationTypeNames[typeID]; ok {
		return name
	}
	return fmt.Sprintf("Unknown (0x%02X)", typeID)
}
