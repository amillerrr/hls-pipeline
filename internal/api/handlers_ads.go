package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/amillerrr/hls-pipeline/internal/ssai"
)

var adsTracer = otel.Tracer("hls-pipeline/api-ads")

// AdsHandler handles SSAI-related endpoints.
type AdsHandler struct {
	mediaTailor *ssai.MediaTailorClient
	logger      *slog.Logger
}

// NewAdsHandler creates a new ads handler.
func NewAdsHandler(mediaTailor *ssai.MediaTailorClient, logger *slog.Logger) *AdsHandler {
	return &AdsHandler{
		mediaTailor: mediaTailor,
		logger:      logger,
	}
}

// RegisterRoutes registers the ads routes with the chi router.
func (h *AdsHandler) RegisterRoutes(r chi.Router) {
	r.Route("/ssai", func(r chi.Router) {
		r.Post("/session", h.CreateSession)
		r.Get("/session/{sessionId}", h.GetSession)
		r.Post("/session/{sessionId}/track", h.TrackAdEvent)
		r.Get("/playback/{videoId}", h.GetPlaybackURL)
		r.Get("/avails/{videoId}", h.GetAvailableSlots)
	})
}

// CreateSessionRequest is the request body for session creation.
type CreateSessionRequest struct {
	VideoID      string            `json:"videoId"`
	ContentPath  string            `json:"contentPath"`
	PlayerParams map[string]string `json:"playerParams,omitempty"`
	AdParams     map[string]string `json:"adParams,omitempty"`
	ViewerID     string            `json:"viewerId,omitempty"`
	DeviceType   string            `json:"deviceType,omitempty"`
}

// CreateSessionResponse is the response for session creation.
type CreateSessionResponse struct {
	SessionID        string            `json:"sessionId"`
	HLSManifestURL   string            `json:"hlsManifestUrl"`
	DASHManifestURL  string            `json:"dashManifestUrl,omitempty"`
	TrackingEndpoint string            `json:"trackingEndpoint"`
	ExpiresAt        time.Time         `json:"expiresAt"`
	PlayerParams     map[string]string `json:"playerParams,omitempty"`
}

// CreateSession creates a new SSAI playback session.
func (h *AdsHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	ctx, span := adsTracer.Start(r.Context(), "create-ssai-session")
	defer span.End()

	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.ErrorContext(ctx, "Failed to decode request", "error", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.VideoID == "" && req.ContentPath == "" {
		http.Error(w, "videoId or contentPath required", http.StatusBadRequest)
		return
	}

	contentPath := req.ContentPath
	if contentPath == "" {
		contentPath = req.VideoID + "/master.m3u8"
	}

	span.SetAttributes(
		attribute.String("video.id", req.VideoID),
		attribute.String("content.path", contentPath),
	)

	// Build session request
	sessionReq := &ssai.SessionRequest{
		ContentID:  req.VideoID,
		ViewerID:   req.ViewerID,
		DeviceType: req.DeviceType,
		AdParams:   req.AdParams,
		SourceURL:  contentPath,
	}

	session, err := h.mediaTailor.CreateSession(ctx, sessionReq)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to create session", "error", err)
		span.RecordError(err)
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	response := CreateSessionResponse{
		SessionID:        session.SessionID,
		HLSManifestURL:   session.ManifestURL,
		TrackingEndpoint: session.TrackingURL,
		ExpiresAt:        session.ExpiresAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetSession retrieves session information.
func (h *AdsHandler) GetSession(w http.ResponseWriter, r *http.Request) {
	ctx, span := adsTracer.Start(r.Context(), "get-ssai-session")
	defer span.End()

	sessionID := chi.URLParam(r, "sessionId")

	if sessionID == "" {
		http.Error(w, "sessionId required", http.StatusBadRequest)
		return
	}

	span.SetAttributes(attribute.String("session.id", sessionID))

	// In a real implementation, you'd retrieve the session from a store
	response := map[string]interface{}{
		"sessionId": sessionID,
		"status":    "active",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// TrackAdEventRequest is the request body for ad tracking.
type TrackAdEventRequest struct {
	EventType string `json:"eventType"`
	AdID      string `json:"adId"`
	Position  int    `json:"position,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

// TrackAdEvent reports an ad tracking event.
func (h *AdsHandler) TrackAdEvent(w http.ResponseWriter, r *http.Request) {
	ctx, span := adsTracer.Start(r.Context(), "track-ad-event")
	defer span.End()

	sessionID := chi.URLParam(r, "sessionId")

	var req TrackAdEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.EventType == "" || req.AdID == "" {
		http.Error(w, "eventType and adId required", http.StatusBadRequest)
		return
	}

	span.SetAttributes(
		attribute.String("session.id", sessionID),
		attribute.String("event.type", req.EventType),
		attribute.String("ad.id", req.AdID),
	)

	// Build tracking URL and report
	trackingURL := h.buildTrackingURL(sessionID, req.EventType, req.AdID)
	if err := h.mediaTailor.ReportAdEvent(ctx, trackingURL); err != nil {
		h.logger.ErrorContext(ctx, "Failed to track ad event", "error", err)
		span.RecordError(err)
		http.Error(w, "Failed to track event", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetPlaybackURLResponse is the response for playback URL requests.
type GetPlaybackURLResponse struct {
	HLSManifestURL  string `json:"hlsManifestUrl"`
	DASHManifestURL string `json:"dashManifestUrl,omitempty"`
	SessionID       string `json:"sessionId,omitempty"`
}

// GetPlaybackURL returns the SSAI-enabled playback URL for a video.
func (h *AdsHandler) GetPlaybackURL(w http.ResponseWriter, r *http.Request) {
	ctx, span := adsTracer.Start(r.Context(), "get-playback-url")
	defer span.End()

	videoID := chi.URLParam(r, "videoId")

	if videoID == "" {
		http.Error(w, "videoId required", http.StatusBadRequest)
		return
	}

	span.SetAttributes(attribute.String("video.id", videoID))

	// Extract ad params from query string
	adParams := make(map[string]string)
	for key, values := range r.URL.Query() {
		if len(values) > 0 && key != "" {
			adParams[key] = values[0]
		}
	}

	sessionReq := &ssai.SessionRequest{
		ContentID: videoID,
		AdParams:  adParams,
		SourceURL: videoID + "/master.m3u8",
	}

	session, err := h.mediaTailor.CreateSession(ctx, sessionReq)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to create playback session", "error", err)
		span.RecordError(err)
		http.Error(w, "Failed to get playback URL", http.StatusInternalServerError)
		return
	}

	response := GetPlaybackURLResponse{
		HLSManifestURL: session.ManifestURL,
		SessionID:      session.SessionID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetAvailableSlotsResponse is the response for available ad slots.
type GetAvailableSlotsResponse struct {
	VideoID   string    `json:"videoId"`
	AdSlots   []AdSlot  `json:"adSlots"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// AdSlot represents an available ad slot.
type AdSlot struct {
	ID           string  `json:"id"`
	StartTime    float64 `json:"startTime"`
	Duration     float64 `json:"duration"`
	Type         string  `json:"type"` // "preroll", "midroll", "postroll"
	SlotPosition int     `json:"slotPosition"`
}

// GetAvailableSlots returns available ad slots for a video.
func (h *AdsHandler) GetAvailableSlots(w http.ResponseWriter, r *http.Request) {
	ctx, span := adsTracer.Start(r.Context(), "get-available-slots")
	defer span.End()

	videoID := chi.URLParam(r, "videoId")

	if videoID == "" {
		http.Error(w, "videoId required", http.StatusBadRequest)
		return
	}

	span.SetAttributes(attribute.String("video.id", videoID))

	// Get ad breaks from MediaTailor
	adBreaks, err := h.mediaTailor.GetTrackingEvents(ctx, videoID)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to get ad slots", "error", err)
		span.RecordError(err)
		http.Error(w, "Failed to get ad slots", http.StatusInternalServerError)
		return
	}

	// Convert to ad slots
	slots := make([]AdSlot, 0, len(adBreaks))
	for i, ab := range adBreaks {
		slotType := "midroll"
		if ab.StartTime.Seconds() == 0 {
			slotType = "preroll"
		}

		slots = append(slots, AdSlot{
			ID:           ab.SpliceEventID.String(),
			StartTime:    ab.StartTime.Seconds(),
			Duration:     ab.Duration.Seconds(),
			Type:         slotType,
			SlotPosition: i + 1,
		})
	}

	response := GetAvailableSlotsResponse{
		VideoID:   videoID,
		AdSlots:   slots,
		UpdatedAt: time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// buildTrackingURL builds the tracking URL for an ad event.
func (h *AdsHandler) buildTrackingURL(sessionID, eventType, adID string) string {
	// This would typically be constructed based on MediaTailor configuration
	return ""
}
