package playlist

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amillerrr/hls-pipeline/internal/config"
)

// DASHConfig contains configuration for DASH manifest generation.
type DASHConfig struct {
	MinBufferTime      float64 `json:"minBufferTime"`
	SegmentDuration    float64 `json:"segmentDuration"`
	Type               string  `json:"type"` // static, dynamic
	AvailabilityStart  *time.Time `json:"availabilityStart,omitempty"`
	TimeShiftBuffer    float64 `json:"timeShiftBuffer,omitempty"`
	SuggestedPresentationDelay float64 `json:"suggestedPresentationDelay,omitempty"`
	MinUpdatePeriod    float64 `json:"minUpdatePeriod,omitempty"`
	UTCTimingURL       string  `json:"utcTimingUrl,omitempty"`
	BaseURL            string  `json:"baseUrl,omitempty"`
}

// DefaultDASHConfig returns the default DASH configuration.
func DefaultDASHConfig() *DASHConfig {
	return &DASHConfig{
		MinBufferTime:   2.0,
		SegmentDuration: 4.0,
		Type:            "static",
	}
}

// DASHGenerator generates DASH manifests.
type DASHGenerator struct {
	config *DASHConfig
}

// NewDASHGenerator creates a new DASH manifest generator.
func NewDASHGenerator(cfg *DASHConfig) *DASHGenerator {
	if cfg == nil {
		cfg = DefaultDASHConfig()
	}
	return &DASHGenerator{config: cfg}
}

// MPD represents a DASH Media Presentation Description.
type MPD struct {
	XMLName                    xml.Name `xml:"MPD"`
	Xmlns                      string   `xml:"xmlns,attr"`
	XmlnsCenc                  string   `xml:"xmlns:cenc,attr,omitempty"`
	XmlnsXsi                   string   `xml:"xmlns:xsi,attr,omitempty"`
	XsiSchemaLocation          string   `xml:"xsi:schemaLocation,attr,omitempty"`
	Type                       string   `xml:"type,attr"`
	MinBufferTime              string   `xml:"minBufferTime,attr"`
	MediaPresentationDuration  string   `xml:"mediaPresentationDuration,attr,omitempty"`
	AvailabilityStartTime      string   `xml:"availabilityStartTime,attr,omitempty"`
	TimeShiftBufferDepth       string   `xml:"timeShiftBufferDepth,attr,omitempty"`
	SuggestedPresentationDelay string   `xml:"suggestedPresentationDelay,attr,omitempty"`
	MinUpdatePeriod            string   `xml:"minUpdatePeriod,attr,omitempty"`
	Profiles                   string   `xml:"profiles,attr"`
	UTCTiming                  *UTCTiming `xml:"UTCTiming,omitempty"`
	Periods                    []Period `xml:"Period"`
}

// UTCTiming represents UTC timing element.
type UTCTiming struct {
	SchemeIdUri string `xml:"schemeIdUri,attr"`
	Value       string `xml:"value,attr,omitempty"`
}

// Period represents a DASH period.
type Period struct {
	ID              string           `xml:"id,attr,omitempty"`
	Start           string           `xml:"start,attr,omitempty"`
	Duration        string           `xml:"duration,attr,omitempty"`
	BaseURL         *BaseURL         `xml:"BaseURL,omitempty"`
	AdaptationSets  []AdaptationSet  `xml:"AdaptationSet"`
}

// BaseURL represents a base URL element.
type BaseURL struct {
	Value string `xml:",chardata"`
}

// AdaptationSet represents a DASH adaptation set.
type AdaptationSet struct {
	ID                  int                 `xml:"id,attr"`
	ContentType         string              `xml:"contentType,attr"`
	MimeType            string              `xml:"mimeType,attr"`
	SegmentAlignment    bool                `xml:"segmentAlignment,attr"`
	StartWithSAP        int                 `xml:"startWithSAP,attr"`
	Lang                string              `xml:"lang,attr,omitempty"`
	Codecs              string              `xml:"codecs,attr,omitempty"`
	ContentProtection   []ContentProtection `xml:"ContentProtection,omitempty"`
	SegmentTemplate     *SegmentTemplate    `xml:"SegmentTemplate,omitempty"`
	Representations     []Representation    `xml:"Representation"`
}

// ContentProtection represents DRM content protection.
type ContentProtection struct {
	SchemeIdUri string `xml:"schemeIdUri,attr"`
	Value       string `xml:"value,attr,omitempty"`
	CencDefaultKID string `xml:"cenc:default_KID,attr,omitempty"`
	PSSH        *PSSH  `xml:"cenc:pssh,omitempty"`
}

// PSSH represents a PSSH box.
type PSSH struct {
	Value string `xml:",chardata"`
}

// SegmentTemplate represents DASH segment template.
type SegmentTemplate struct {
	Timescale        int    `xml:"timescale,attr"`
	Duration         int    `xml:"duration,attr,omitempty"`
	Initialization   string `xml:"initialization,attr"`
	Media            string `xml:"media,attr"`
	StartNumber      int    `xml:"startNumber,attr,omitempty"`
	SegmentTimeline  *SegmentTimeline `xml:"SegmentTimeline,omitempty"`
}

// SegmentTimeline represents segment timeline for variable duration segments.
type SegmentTimeline struct {
	S []TimelineSegment `xml:"S"`
}

// TimelineSegment represents a segment in the timeline.
type TimelineSegment struct {
	T int `xml:"t,attr,omitempty"` // Time
	D int `xml:"d,attr"`          // Duration
	R int `xml:"r,attr,omitempty"` // Repeat count
}

// Representation represents a DASH representation.
type Representation struct {
	ID              string           `xml:"id,attr"`
	Bandwidth       int64            `xml:"bandwidth,attr"`
	Width           int              `xml:"width,attr,omitempty"`
	Height          int              `xml:"height,attr,omitempty"`
	FrameRate       string           `xml:"frameRate,attr,omitempty"`
	Codecs          string           `xml:"codecs,attr,omitempty"`
	BaseURL         *BaseURL         `xml:"BaseURL,omitempty"`
	SegmentTemplate *SegmentTemplate `xml:"SegmentTemplate,omitempty"`
}

// GenerateManifest generates a DASH MPD manifest.
func (g *DASHGenerator) GenerateManifest(outputDir string, presets []config.PresetConfig, duration float64) error {
	mpd := g.buildMPD(presets, duration)

	output, err := xml.MarshalIndent(mpd, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal MPD: %w", err)
	}

	// Add XML declaration
	xmlDecl := []byte(xml.Header)
	output = append(xmlDecl, output...)

	manifestPath := filepath.Join(outputDir, "manifest.mpd")
	return os.WriteFile(manifestPath, output, 0644)
}

// buildMPD constructs the MPD structure.
func (g *DASHGenerator) buildMPD(presets []config.PresetConfig, duration float64) *MPD {
	mpd := &MPD{
		Xmlns:                   "urn:mpeg:dash:schema:mpd:2011",
		XmlnsCenc:               "urn:mpeg:cenc:2013",
		Type:                    g.config.Type,
		MinBufferTime:          formatDuration(g.config.MinBufferTime),
		Profiles:               "urn:mpeg:dash:profile:isoff-live:2011,urn:mpeg:dash:profile:cmaf:2019",
	}

	if g.config.Type == "static" && duration > 0 {
		mpd.MediaPresentationDuration = formatDuration(duration)
	}

	if g.config.Type == "dynamic" {
		if g.config.AvailabilityStart != nil {
			mpd.AvailabilityStartTime = g.config.AvailabilityStart.Format(time.RFC3339)
		}
		if g.config.TimeShiftBuffer > 0 {
			mpd.TimeShiftBufferDepth = formatDuration(g.config.TimeShiftBuffer)
		}
		if g.config.SuggestedPresentationDelay > 0 {
			mpd.SuggestedPresentationDelay = formatDuration(g.config.SuggestedPresentationDelay)
		}
		if g.config.MinUpdatePeriod > 0 {
			mpd.MinUpdatePeriod = formatDuration(g.config.MinUpdatePeriod)
		}
	}

	if g.config.UTCTimingURL != "" {
		mpd.UTCTiming = &UTCTiming{
			SchemeIdUri: "urn:mpeg:dash:utc:http-xsdate:2014",
			Value:       g.config.UTCTimingURL,
		}
	}

	// Create period
	period := Period{
		ID:    "0",
		Start: "PT0S",
	}

	if g.config.BaseURL != "" {
		period.BaseURL = &BaseURL{Value: g.config.BaseURL}
	}

	// Video adaptation set
	videoAS := AdaptationSet{
		ID:               0,
		ContentType:      "video",
		MimeType:         "video/mp4",
		SegmentAlignment: true,
		StartWithSAP:     1,
		SegmentTemplate: &SegmentTemplate{
			Timescale:      1000,
			Duration:       int(g.config.SegmentDuration * 1000),
			Initialization: "$RepresentationID$/init.mp4",
			Media:          "$RepresentationID$/seg_$Number%05d$.m4s",
			StartNumber:    0,
		},
	}

	for _, preset := range presets {
		bandwidth := preset.Bandwidth()
		codecs := config.GetAVCCodecString(preset.Profile, preset.Level)

		rep := Representation{
			ID:        preset.Name,
			Bandwidth: bandwidth,
			Width:     preset.Width,
			Height:    preset.Height,
			FrameRate: fmt.Sprintf("%.0f", preset.FrameRate),
			Codecs:    codecs,
			BaseURL:   &BaseURL{Value: preset.Name + "/"},
		}

		videoAS.Representations = append(videoAS.Representations, rep)
	}

	// Audio adaptation set
	audioAS := AdaptationSet{
		ID:               1,
		ContentType:      "audio",
		MimeType:         "audio/mp4",
		SegmentAlignment: true,
		StartWithSAP:     1,
		Lang:             "en",
		SegmentTemplate: &SegmentTemplate{
			Timescale:      1000,
			Duration:       int(g.config.SegmentDuration * 1000),
			Initialization: "audio/init.mp4",
			Media:          "audio/seg_$Number%05d$.m4s",
			StartNumber:    0,
		},
		Representations: []Representation{
			{
				ID:        "audio",
				Bandwidth: 128000,
				Codecs:    "mp4a.40.2",
			},
		},
	}

	period.AdaptationSets = []AdaptationSet{videoAS, audioAS}
	mpd.Periods = []Period{period}

	return mpd
}

// GenerateManifestWithDRM generates a DASH manifest with DRM content protection.
func (g *DASHGenerator) GenerateManifestWithDRM(
	outputDir string,
	presets []config.PresetConfig,
	duration float64,
	drmSystems []DRMSystemInfo,
) error {
	mpd := g.buildMPD(presets, duration)

	// Add content protection to video adaptation set
	if len(mpd.Periods) > 0 && len(mpd.Periods[0].AdaptationSets) > 0 {
		var contentProtections []ContentProtection

		for _, drm := range drmSystems {
			cp := ContentProtection{
				SchemeIdUri:    drm.SchemeURI,
				Value:          drm.Value,
				CencDefaultKID: drm.DefaultKID,
			}
			if drm.PSSH != "" {
				cp.PSSH = &PSSH{Value: drm.PSSH}
			}
			contentProtections = append(contentProtections, cp)
		}

		mpd.Periods[0].AdaptationSets[0].ContentProtection = contentProtections
	}

	output, err := xml.MarshalIndent(mpd, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal MPD: %w", err)
	}

	xmlDecl := []byte(xml.Header)
	output = append(xmlDecl, output...)

	manifestPath := filepath.Join(outputDir, "manifest.mpd")
	return os.WriteFile(manifestPath, output, 0644)
}

// DRMSystemInfo contains DRM system information for DASH.
type DRMSystemInfo struct {
	SchemeURI  string `json:"schemeUri"`
	Value      string `json:"value,omitempty"`
	DefaultKID string `json:"defaultKid,omitempty"`
	PSSH       string `json:"pssh,omitempty"`
}

// formatDuration formats a duration in seconds to ISO 8601 duration format.
func formatDuration(seconds float64) string {
	if seconds == 0 {
		return "PT0S"
	}

	var parts []string
	parts = append(parts, "PT")

	hours := int(seconds) / 3600
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dH", hours))
		seconds -= float64(hours * 3600)
	}

	minutes := int(seconds) / 60
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dM", minutes))
		seconds -= float64(minutes * 60)
	}

	if seconds > 0 || len(parts) == 1 {
		parts = append(parts, fmt.Sprintf("%.3fS", seconds))
	}

	return strings.Join(parts, "")
}

// GenerateSimpleMPD generates a simple DASH manifest (non-XML builder version).
func GenerateSimpleMPD(outputPath string, presets []config.PresetConfig, segmentDuration float64) error {
	var mpd strings.Builder

	mpd.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	mpd.WriteString(`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" `)
	mpd.WriteString(`xmlns:cenc="urn:mpeg:cenc:2013" `)
	mpd.WriteString(`type="static" `)
	mpd.WriteString(fmt.Sprintf(`minBufferTime="PT%.1fS" `, segmentDuration))
	mpd.WriteString(`profiles="urn:mpeg:dash:profile:isoff-live:2011,urn:mpeg:dash:profile:cmaf:2019">` + "\n")

	mpd.WriteString(`  <Period id="0" start="PT0S">` + "\n")

	// Video adaptation set
	mpd.WriteString(`    <AdaptationSet mimeType="video/mp4" segmentAlignment="true" `)
	mpd.WriteString(`startWithSAP="1" contentType="video">` + "\n")

	for _, preset := range presets {
		bandwidth := config.ParseBitrate(preset.VideoBitrate)
		codecs := config.GetAVCCodecString(preset.Profile, preset.Level)

		mpd.WriteString(fmt.Sprintf(
			`      <Representation id="%s" bandwidth="%d" width="%d" height="%d" `+
				`codecs="%s" frameRate="%.0f">`+"\n",
			preset.Name, bandwidth, preset.Width, preset.Height, codecs, preset.FrameRate,
		))
		mpd.WriteString(fmt.Sprintf(`        <BaseURL>%s/</BaseURL>`+"\n", preset.Name))
		mpd.WriteString(`        <SegmentTemplate initialization="init.mp4" `)
		mpd.WriteString(`media="seg_$Number%05d$.m4s" startNumber="0" `)
		mpd.WriteString(fmt.Sprintf(`timescale="1000" duration="%d"/>`+"\n",
			int(segmentDuration*1000)))
		mpd.WriteString(`      </Representation>` + "\n")
	}

	mpd.WriteString(`    </AdaptationSet>` + "\n")

	// Audio adaptation set
	mpd.WriteString(`    <AdaptationSet mimeType="audio/mp4" segmentAlignment="true" `)
	mpd.WriteString(`startWithSAP="1" contentType="audio" lang="en">` + "\n")
	mpd.WriteString(`      <Representation id="audio" bandwidth="128000" codecs="mp4a.40.2">` + "\n")
	mpd.WriteString(`        <SegmentTemplate initialization="init_audio.mp4" `)
	mpd.WriteString(`media="audio_$Number%05d$.m4s" startNumber="0" `)
	mpd.WriteString(fmt.Sprintf(`timescale="1000" duration="%d"/>`+"\n",
		int(segmentDuration*1000)))
	mpd.WriteString(`      </Representation>` + "\n")
	mpd.WriteString(`    </AdaptationSet>` + "\n")

	mpd.WriteString(`  </Period>` + "\n")
	mpd.WriteString(`</MPD>` + "\n")

	return os.WriteFile(outputPath, []byte(mpd.String()), 0644)
}
