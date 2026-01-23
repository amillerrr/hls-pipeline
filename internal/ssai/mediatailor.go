package ssai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("ssai-mediatailor")

// MediaTailorConfig contains MediaTailor configuration.
type MediaTailorConfig struct {
	// PlaybackConfigurationName is the MediaTailor configuration name.
	PlaybackConfigurationName string `json:"playbackConfigurationName"`

	// Region is the AWS region where MediaTailor is deployed.
	Region string `json:"region"`

	// SessionEndpoint is the session initialization endpoint.
	SessionEndpoint string `json:"sessionEndpoint"`

	// PlaybackEndpoint is the HLS playback endpoint.
	PlaybackEndpoint string `json:"playbackEndpoint"`

	// DASHEndpoint is the DASH playback endpoint.
	DASHEndpoint string `json:"dashEndpoint,omitempty"`

	// AdDecisionServerURL is the ADS URL for VAST/VMAP requests.
	AdDecisionServerURL string `json:"adDecisionServerUrl"`

	// SlateAdURL is the slate ad URL for unfilled breaks.
	SlateAdURL string `json:"slateAdUrl,omitempty"`

	// CDNContentSegmentPrefix is the CDN prefix for content segments.
	CDNContentSegmentPrefix string `json:"cdnContentSegmentPrefix"`

	// CDNAdSegmentPrefix is the CDN prefix for ad segments.
	CDNAdSegmentPrefix string `json:"cdnAdSegmentPrefix"`

	// PersonalizationThreshold is the minimum fill rate (0-100).
	PersonalizationThreshold int `json:"personalizationThreshold"`

	// MaxDuration is the maximum ad break duration in seconds.
	MaxDuration int `json:"maxDuration"`

	// Timeout for API requests.
	Timeout time.Duration `json:"timeout"`
}

// Session represents a MediaTailor playback session.
type Session struct {
	// SessionID is the unique session identifier.
	SessionID string `json:"sessionId"`

	// ManifestEndpoint is the personalized manifest URL.
	ManifestEndpoint string `json:"manifestEndpoint"`

	// TrackingEndpoint is the ad tracking URL.
	TrackingEndpoint string `json:"trackingEndpoint"`

	// DASHManifestEndpoint is the DASH manifest URL (if available).
	DASHManifestEndpoint string `json:"dashManifestEndpoint,omitempty"`

	// ExpiresAt is when the session expires.
	ExpiresAt time.Time `json:"expiresAt"`

	// PlayerParams contains player-specific parameters.
	PlayerParams map[string]string `json:"playerParams,omitempty"`

	// AdParams contains ad-targeting parameters.
	AdParams map[string]string `json:"adParams,omitempty"`
}

// AdBreak represents an ad break in the stream.
type AdBreak struct {
	// ID is the unique identifier for this ad break.
	ID string `json:"id"`

	// StartTime is when the ad break starts (relative to content).
	StartTime time.Duration `json:"startTime"`

	// Duration is the total ad break duration.
	Duration time.Duration `json:"duration"`

	// Ads contains the individual ads in this break.
	Ads []Ad `json:"ads"`

	// AvailNum is the avail number.
	AvailNum int `json:"availNum"`

	// Type is the ad break type (preroll, midroll, postroll).
	Type AdBreakType `json:"type"`
}

// AdBreakType represents the type of ad break.
type AdBreakType string

const (
	AdBreakTypePreroll  AdBreakType = "preroll"
	AdBreakTypeMidroll  AdBreakType = "midroll"
	AdBreakTypePostroll AdBreakType = "postroll"
)

// Ad represents a single ad within an ad break.
type Ad struct {
	// ID is the ad identifier.
	ID string `json:"id"`

	// Duration is the ad duration.
	Duration time.Duration `json:"duration"`

	// AdSystem is the ad system name (e.g., "GDFP").
	AdSystem string `json:"adSystem,omitempty"`

	// CreativeID is the creative identifier.
	CreativeID string `json:"creativeId,omitempty"`

	// AdTitle is the ad title.
	AdTitle string `json:"adTitle,omitempty"`

	// TrackingEvents contains tracking URLs for various events.
	TrackingEvents map[string][]string `json:"trackingEvents,omitempty"`

	// ClickThrough is the click-through URL.
	ClickThrough string `json:"clickThrough,omitempty"`
}

// Client handles MediaTailor API interactions.
type Client struct {
	config     *MediaTailorConfig
	httpClient *http.Client
	logger     *slog.Logger
}

// NewClient creates a new MediaTailor client.
func NewClient(config *MediaTailorConfig, logger *slog.Logger) *Client {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	return &Client{
		config: config,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger: logger,
	}
}

// CreateSession creates a new playback session.
func (c *Client) CreateSession(ctx context.Context, contentPath string, params SessionParams) (*Session, error) {
	ctx, span := tracer.Start(ctx, "mediatailor-create-session",
		trace.WithAttributes(
			attribute.String("content.path", contentPath),
		))
	defer span.End()

	// Build session initialization URL
	sessionURL, err := url.Parse(c.config.SessionEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid session endpoint: %w", err)
	}

	// Add content path
	sessionURL.Path = sessionURL.Path + "/" + strings.TrimPrefix(contentPath, "/")

	// Add query parameters for ad targeting
	q := sessionURL.Query()
	for k, v := range params.AdParams {
		q.Set("ads."+k, v)
	}
	for k, v := range params.PlayerParams {
		q.Set("player."+k, v)
	}
	sessionURL.RawQuery = q.Encode()

	c.logger.DebugContext(ctx, "Creating MediaTailor session",
		"url", sessionURL.String(),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sessionURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create session request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("session request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("session request returned %d: %s", resp.StatusCode, string(body))
	}

	var sessionResp sessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&sessionResp); err != nil {
		return nil, fmt.Errorf("failed to decode session response: %w", err)
	}

	session := &Session{
		SessionID:            sessionResp.SessionID,
		ManifestEndpoint:     sessionResp.ManifestEndpointPrefix + "/" + contentPath,
		TrackingEndpoint:     sessionResp.TrackingEndpointPrefix,
		DASHManifestEndpoint: sessionResp.DASHManifestEndpointPrefix,
		ExpiresAt:            time.Now().Add(24 * time.Hour), // MediaTailor sessions typically last 24h
		PlayerParams:         params.PlayerParams,
		AdParams:             params.AdParams,
	}

	span.SetAttributes(
		attribute.String("session.id", session.SessionID),
	)

	c.logger.InfoContext(ctx, "MediaTailor session created",
		"sessionId", session.SessionID,
		"manifest", session.ManifestEndpoint,
	)

	return session, nil
}

// SessionParams contains parameters for session creation.
type SessionParams struct {
	// PlayerParams contains player-specific parameters.
	PlayerParams map[string]string `json:"playerParams,omitempty"`

	// AdParams contains ad-targeting parameters.
	AdParams map[string]string `json:"adParams,omitempty"`

	// ViewerID is an optional viewer identifier.
	ViewerID string `json:"viewerId,omitempty"`

	// DeviceType is the device type (e.g., "mobile", "ctv", "desktop").
	DeviceType string `json:"deviceType,omitempty"`
}

// sessionResponse is the MediaTailor session initialization response.
type sessionResponse struct {
	SessionID                  string `json:"sessionId"`
	ManifestEndpointPrefix     string `json:"manifestEndpointPrefix"`
	TrackingEndpointPrefix     string `json:"trackingEndpointPrefix"`
	DASHManifestEndpointPrefix string `json:"dashManifestEndpointPrefix,omitempty"`
}

// GetPlaybackURL returns the personalized playback URL for a session.
func (c *Client) GetPlaybackURL(session *Session) string {
	return session.ManifestEndpoint
}

// GetDASHPlaybackURL returns the DASH playback URL for a session.
func (c *Client) GetDASHPlaybackURL(session *Session) string {
	if session.DASHManifestEndpoint != "" {
		return session.DASHManifestEndpoint
	}
	// Convert HLS URL to DASH if available
	return strings.Replace(session.ManifestEndpoint, ".m3u8", ".mpd", 1)
}

// ReportAdTracking sends ad tracking events.
func (c *Client) ReportAdTracking(ctx context.Context, session *Session, eventType string, adID string) error {
	ctx, span := tracer.Start(ctx, "mediatailor-track-ad",
		trace.WithAttributes(
			attribute.String("session.id", session.SessionID),
			attribute.String("event.type", eventType),
			attribute.String("ad.id", adID),
		))
	defer span.End()

	trackingURL := fmt.Sprintf("%s/%s/%s/%s",
		session.TrackingEndpoint,
		session.SessionID,
		adID,
		eventType,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trackingURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create tracking request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("tracking request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("tracking request returned %d", resp.StatusCode)
	}

	c.logger.DebugContext(ctx, "Ad tracking event sent",
		"event", eventType,
		"adId", adID,
	)

	return nil
}

// ParseAdBreaksFromManifest extracts ad breaks from an HLS manifest.
func (c *Client) ParseAdBreaksFromManifest(ctx context.Context, manifest string) ([]AdBreak, error) {
	_, span := tracer.Start(ctx, "mediatailor-parse-ad-breaks")
	defer span.End()

	var breaks []AdBreak

	// Pattern for EXT-X-CUE-OUT
	cueOutRe := regexp.MustCompile(`#EXT-X-CUE-OUT:DURATION=([\d.]+)`)
	// Pattern for EXT-X-DATERANGE with SCTE35
	dateRangeRe := regexp.MustCompile(`#EXT-X-DATERANGE:ID="([^"]+)".*DURATION=([\d.]+).*CLASS="([^"]*)"`)

	lines := strings.Split(manifest, "\n")
	var currentTime time.Duration
	var currentBreak *AdBreak
	breakID := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Track time from EXTINF
		if strings.HasPrefix(line, "#EXTINF:") {
			parts := strings.Split(strings.TrimPrefix(line, "#EXTINF:"), ",")
			if len(parts) > 0 {
				var duration float64
				fmt.Sscanf(parts[0], "%f", &duration)
				currentTime += time.Duration(duration * float64(time.Second))
			}
			continue
		}

		// EXT-X-CUE-OUT
		if matches := cueOutRe.FindStringSubmatch(line); len(matches) > 1 {
			var duration float64
			fmt.Sscanf(matches[1], "%f", &duration)

			breakID++
			breakType := AdBreakTypeMidroll
			if currentTime == 0 {
				breakType = AdBreakTypePreroll
			}

			currentBreak = &AdBreak{
				ID:        fmt.Sprintf("break-%d", breakID),
				StartTime: currentTime,
				Duration:  time.Duration(duration * float64(time.Second)),
				Type:      breakType,
				Ads:       []Ad{},
			}
			breaks = append(breaks, *currentBreak)
			continue
		}

		// EXT-X-DATERANGE
		if matches := dateRangeRe.FindStringSubmatch(line); len(matches) > 2 {
			var duration float64
			fmt.Sscanf(matches[2], "%f", &duration)

			breakType := AdBreakTypeMidroll
			if strings.Contains(matches[3], "preroll") {
				breakType = AdBreakTypePreroll
			} else if strings.Contains(matches[3], "postroll") {
				breakType = AdBreakTypePostroll
			}

			breakID++
			breaks = append(breaks, AdBreak{
				ID:        matches[1],
				StartTime: currentTime,
				Duration:  time.Duration(duration * float64(time.Second)),
				Type:      breakType,
				Ads:       []Ad{},
			})
		}
	}

	span.SetAttributes(attribute.Int("ad_breaks.count", len(breaks)))

	return breaks, nil
}

// InjectAdMarkers injects SCTE-35 ad markers into an HLS manifest.
func (c *Client) InjectAdMarkers(ctx context.Context, manifest string, markers []AdMarker) (string, error) {
	_, span := tracer.Start(ctx, "mediatailor-inject-markers",
		trace.WithAttributes(
			attribute.Int("markers.count", len(markers)),
		))
	defer span.End()

	if len(markers) == 0 {
		return manifest, nil
	}

	lines := strings.Split(manifest, "\n")
	var result []string
	var currentTime time.Duration
	markerIdx := 0

	for _, line := range lines {
		// Track time
		if strings.HasPrefix(line, "#EXTINF:") {
			parts := strings.Split(strings.TrimPrefix(line, "#EXTINF:"), ",")
			if len(parts) > 0 {
				var duration float64
				fmt.Sscanf(parts[0], "%f", &duration)

				// Check if we need to insert a marker before this segment
				for markerIdx < len(markers) && markers[markerIdx].Time <= currentTime {
					marker := markers[markerIdx]
					result = append(result, marker.ToHLSTags()...)
					markerIdx++
				}

				currentTime += time.Duration(duration * float64(time.Second))
			}
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n"), nil
}

// AdMarker represents a point where ads can be inserted.
type AdMarker struct {
	// Time is when the marker occurs.
	Time time.Duration `json:"time"`

	// Duration is the ad break duration.
	Duration time.Duration `json:"duration"`

	// ID is an optional marker ID.
	ID string `json:"id,omitempty"`

	// Type is the marker type.
	Type AdBreakType `json:"type"`

	// SCTE35 is the optional SCTE-35 payload (base64).
	SCTE35 string `json:"scte35,omitempty"`
}

// ToHLSTags converts the marker to HLS tags.
func (m *AdMarker) ToHLSTags() []string {
	var tags []string

	// Add DATERANGE tag for modern players
	if m.ID != "" {
		tags = append(tags, fmt.Sprintf(
			"#EXT-X-DATERANGE:ID=\"%s\",CLASS=\"%s\",START-DATE=\"%s\",DURATION=%.3f",
			m.ID,
			m.Type,
			time.Now().UTC().Format(time.RFC3339),
			m.Duration.Seconds(),
		))
	}

	// Add CUE-OUT for compatibility
	tags = append(tags, fmt.Sprintf("#EXT-X-CUE-OUT:DURATION=%.3f", m.Duration.Seconds()))

	// Add SCTE35 if available
	if m.SCTE35 != "" {
		tags = append(tags, fmt.Sprintf("#EXT-OATCLS-SCTE35:%s", m.SCTE35))
	}

	return tags
}

// GetAvailableSlots returns the ad slots (avails) in content.
func (c *Client) GetAvailableSlots(ctx context.Context, contentDuration time.Duration, slots []time.Duration) []AdMarker {
	markers := make([]AdMarker, 0, len(slots)+2)

	// Add preroll slot
	markers = append(markers, AdMarker{
		Time:     0,
		Duration: time.Duration(c.config.MaxDuration) * time.Second,
		ID:       "preroll",
		Type:     AdBreakTypePreroll,
	})

	// Add midroll slots
	for i, slotTime := range slots {
		if slotTime > 0 && slotTime < contentDuration {
			markers = append(markers, AdMarker{
				Time:     slotTime,
				Duration: time.Duration(c.config.MaxDuration) * time.Second,
				ID:       fmt.Sprintf("midroll-%d", i+1),
				Type:     AdBreakTypeMidroll,
			})
		}
	}

	// Add postroll slot
	markers = append(markers, AdMarker{
		Time:     contentDuration,
		Duration: time.Duration(c.config.MaxDuration) * time.Second,
		ID:       "postroll",
		Type:     AdBreakTypePostroll,
	})

	return markers
}
