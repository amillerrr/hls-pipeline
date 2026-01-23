package ssai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mediatailor"
	"github.com/aws/aws-sdk-go-v2/service/mediatailor/types"
)

// MediaTailorConfig contains MediaTailor configuration.
type MediaTailorConfig struct {
	Region                  string `json:"region"`
	PlaybackConfigName      string `json:"playbackConfigName"`
	AdDecisionServerURL     string `json:"adDecisionServerUrl"`
	VideoContentSourceURL   string `json:"videoContentSourceUrl"`
	SlateAdURL              string `json:"slateAdUrl,omitempty"`
	PersonalizationThreshold int   `json:"personalizationThreshold,omitempty"`
	MaxDuration             int    `json:"maxDuration,omitempty"`
	CDNContentSegmentPrefix string `json:"cdnContentSegmentPrefix,omitempty"`
	CDNAdSegmentPrefix      string `json:"cdnAdSegmentPrefix,omitempty"`
}

// MediaTailorClient handles MediaTailor operations.
type MediaTailorClient struct {
	client     *mediatailor.Client
	httpClient *http.Client
	config     *MediaTailorConfig
}

// NewMediaTailorClient creates a new MediaTailor client.
func NewMediaTailorClient(awsCfg aws.Config, cfg *MediaTailorConfig) *MediaTailorClient {
	return &MediaTailorClient{
		client: mediatailor.NewFromConfig(awsCfg),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		config: cfg,
	}
}

// SessionRequest contains parameters for creating a session.
type SessionRequest struct {
	// ContentID identifies the content being played.
	ContentID string `json:"contentId"`
	
	// AdParams contains ad targeting parameters.
	AdParams map[string]string `json:"adParams,omitempty"`
	
	// ViewerID identifies the viewer (for personalization).
	ViewerID string `json:"viewerId,omitempty"`
	
	// DeviceType for device-specific ad targeting.
	DeviceType string `json:"deviceType,omitempty"`
	
	// SourceURL is the original manifest URL.
	SourceURL string `json:"sourceUrl,omitempty"`
}

// SessionResponse contains the session information.
type SessionResponse struct {
	// SessionID is the unique session identifier.
	SessionID string `json:"sessionId"`
	
	// ManifestURL is the personalized manifest URL.
	ManifestURL string `json:"manifestUrl"`
	
	// TrackingURL is the URL for ad tracking events.
	TrackingURL string `json:"trackingUrl,omitempty"`
	
	// ExpiresAt is when the session expires.
	ExpiresAt time.Time `json:"expiresAt"`
}

// CreateSession creates a new MediaTailor session.
func (c *MediaTailorClient) CreateSession(ctx context.Context, req *SessionRequest) (*SessionResponse, error) {
	// Build the session initialization URL
	baseURL := c.buildSessionURL(req)

	// Make the session request
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create session request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("session creation failed with status %d", resp.StatusCode)
	}

	// Parse session response from headers/body
	session := &SessionResponse{
		ManifestURL: baseURL,
		ExpiresAt:   time.Now().Add(24 * time.Hour), // Default expiry
	}

	// Extract session ID from response headers if available
	if sessionID := resp.Header.Get("X-MediaPackage-Session-Id"); sessionID != "" {
		session.SessionID = sessionID
	}

	return session, nil
}

// buildSessionURL builds the session initialization URL.
func (c *MediaTailorClient) buildSessionURL(req *SessionRequest) string {
	// MediaTailor URL format:
	// https://mediatailor.{region}.amazonaws.com/v1/session/{hashed-account-id}/{config-name}/master.m3u8

	baseURL := fmt.Sprintf("https://mediatailor.%s.amazonaws.com/v1/session/%s/master.m3u8",
		c.config.Region,
		c.config.PlaybackConfigName,
	)

	// Add ad targeting parameters
	params := url.Values{}

	if req.ViewerID != "" {
		params.Set("ads.viewerId", req.ViewerID)
	}
	if req.DeviceType != "" {
		params.Set("ads.deviceType", req.DeviceType)
	}
	if req.ContentID != "" {
		params.Set("ads.contentId", req.ContentID)
	}

	// Add custom ad parameters
	for key, value := range req.AdParams {
		params.Set("ads."+key, value)
	}

	if len(params) > 0 {
		baseURL += "?" + params.Encode()
	}

	return baseURL
}

// CreatePlaybackConfiguration creates a MediaTailor playback configuration.
func (c *MediaTailorClient) CreatePlaybackConfiguration(ctx context.Context) error {
	input := &mediatailor.PutPlaybackConfigurationInput{
		Name:                    aws.String(c.config.PlaybackConfigName),
		AdDecisionServerUrl:     aws.String(c.config.AdDecisionServerURL),
		VideoContentSourceUrl:   aws.String(c.config.VideoContentSourceURL),
	}

	if c.config.SlateAdURL != "" {
		input.SlateAdUrl = aws.String(c.config.SlateAdURL)
	}

	if c.config.PersonalizationThreshold > 0 {
		input.PersonalizationThresholdSeconds = aws.Int32(int32(c.config.PersonalizationThreshold))
	}

	// CDN configuration
	if c.config.CDNContentSegmentPrefix != "" || c.config.CDNAdSegmentPrefix != "" {
		input.CdnConfiguration = &types.CdnConfiguration{}
		if c.config.CDNContentSegmentPrefix != "" {
			input.CdnConfiguration.ContentSegmentUrlPrefix = aws.String(c.config.CDNContentSegmentPrefix)
		}
		if c.config.CDNAdSegmentPrefix != "" {
			input.CdnConfiguration.AdSegmentUrlPrefix = aws.String(c.config.CDNAdSegmentPrefix)
		}
	}

	_, err := c.client.PutPlaybackConfiguration(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to create playback configuration: %w", err)
	}

	return nil
}

// GetPlaybackConfiguration gets a MediaTailor playback configuration.
func (c *MediaTailorClient) GetPlaybackConfiguration(ctx context.Context) (*types.PlaybackConfiguration, error) {
	input := &mediatailor.GetPlaybackConfigurationInput{
		Name: aws.String(c.config.PlaybackConfigName),
	}

	output, err := c.client.GetPlaybackConfiguration(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get playback configuration: %w", err)
	}

	return &types.PlaybackConfiguration{
		Name:                                output.Name,
		AdDecisionServerUrl:                 output.AdDecisionServerUrl,
		VideoContentSourceUrl:               output.VideoContentSourceUrl,
		SlateAdUrl:                          output.SlateAdUrl,
		HlsConfiguration:                    output.HlsConfiguration,
		DashConfiguration:                   output.DashConfiguration,
		PlaybackConfigurationArn:            output.PlaybackConfigurationArn,
		PlaybackEndpointPrefix:              output.PlaybackEndpointPrefix,
		SessionInitializationEndpointPrefix: output.SessionInitializationEndpointPrefix,
	}, nil
}

// DeletePlaybackConfiguration deletes a MediaTailor playback configuration.
func (c *MediaTailorClient) DeletePlaybackConfiguration(ctx context.Context) error {
	input := &mediatailor.DeletePlaybackConfigurationInput{
		Name: aws.String(c.config.PlaybackConfigName),
	}

	_, err := c.client.DeletePlaybackConfiguration(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to delete playback configuration: %w", err)
	}

	return nil
}

// AdBreak represents an ad break in the content.
type AdBreak struct {
	StartTime       time.Duration `json:"startTime"`
	Duration        time.Duration `json:"duration"`
	Ads             []Ad          `json:"ads,omitempty"`
	SpliceEventID   uint32        `json:"spliceEventId,omitempty"`
	AvailNum        int           `json:"availNum,omitempty"`
	AvailsExpected  int           `json:"availsExpected,omitempty"`
}

// Ad represents a single ad within a break.
type Ad struct {
	AdID            string        `json:"adId"`
	Duration        time.Duration `json:"duration"`
	CreativeID      string        `json:"creativeId,omitempty"`
	AdSystem        string        `json:"adSystem,omitempty"`
	AdTitle         string        `json:"adTitle,omitempty"`
	TrackingEvents  []TrackingEvent `json:"trackingEvents,omitempty"`
}

// TrackingEvent represents an ad tracking event.
type TrackingEvent struct {
	Event string `json:"event"` // impression, start, firstQuartile, midpoint, thirdQuartile, complete
	URL   string `json:"url"`
}

// GetTrackingEvents fetches tracking events for a session.
func (c *MediaTailorClient) GetTrackingEvents(ctx context.Context, sessionID string) ([]AdBreak, error) {
	trackingURL := fmt.Sprintf("https://mediatailor.%s.amazonaws.com/v1/tracking/%s/%s",
		c.config.Region,
		c.config.PlaybackConfigName,
		sessionID,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trackingURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create tracking request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get tracking events: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracking request failed with status %d", resp.StatusCode)
	}

	var adBreaks []AdBreak
	if err := json.NewDecoder(resp.Body).Decode(&adBreaks); err != nil {
		return nil, fmt.Errorf("failed to decode tracking response: %w", err)
	}

	return adBreaks, nil
}

// ReportAdEvent reports an ad tracking event.
func (c *MediaTailorClient) ReportAdEvent(ctx context.Context, trackingURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trackingURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create tracking request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to report ad event: %w", err)
	}
	defer resp.Body.Close()

	// Tracking endpoints typically return 200 or 204
	if resp.StatusCode >= 300 {
		return fmt.Errorf("tracking request failed with status %d", resp.StatusCode)
	}

	return nil
}

// InsertAdBreakMarkers inserts SCTE-35 markers for ad breaks into a playlist.
func InsertAdBreakMarkers(playlist string, adBreaks []AdBreak) string {
	if len(adBreaks) == 0 {
		return playlist
	}

	lines := strings.Split(playlist, "\n")
	var result strings.Builder

	var currentTime float64
	breakIdx := 0

	for _, line := range lines {
		// Track segment durations
		if strings.HasPrefix(line, "#EXTINF:") {
			var duration float64
			fmt.Sscanf(line, "#EXTINF:%f", &duration)

			// Check if we need to insert an ad break marker
			for breakIdx < len(adBreaks) {
				breakTime := adBreaks[breakIdx].StartTime.Seconds()
				if breakTime >= currentTime && breakTime < currentTime+duration {
					// Insert cue-out marker
					result.WriteString(InsertCueOutTag(adBreaks[breakIdx].Duration.Seconds()) + "\n")
					breakIdx++
				} else {
					break
				}
			}

			currentTime += duration
		}

		result.WriteString(line + "\n")
	}

	return result.String()
}

// ValidateMediaTailorConfig validates MediaTailor configuration.
func ValidateMediaTailorConfig(cfg *MediaTailorConfig) error {
	if cfg.PlaybackConfigName == "" {
		return fmt.Errorf("playbackConfigName is required")
	}
	if cfg.AdDecisionServerURL == "" {
		return fmt.Errorf("adDecisionServerUrl is required")
	}
	if cfg.VideoContentSourceURL == "" {
		return fmt.Errorf("videoContentSourceUrl is required")
	}
	if cfg.Region == "" {
		return fmt.Errorf("region is required")
	}
	return nil
}

