package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"hls-pipeline/internal/ssai"
)

var adsTracer = otel.Tracer("api-ads")

// AdsHandler handles SSAI-related endpoints.
type AdsHandler struct {
	mediaTailor *ssai.Client
	logger      *slog.Logger
}

// NewAdsHandler creates a new ads handler.
func NewAdsHandler(mediaTailor *ssai.Client, logger *slog.Logger) *AdsHandler {
	return &AdsHandler{
		mediaTailor: mediaTailor,
		logger:      logger,
	}
}

// RegisterRoutes registers the ads routes with the router.
func (h *AdsHandler) RegisterRoutes(r *mux.Router) {
	r.HandleFunc("/ssai/session", h.CreateSession).Methods(http.MethodPost)
	r.HandleFunc("/ssai/session/{sessionId}", h.GetSession).Methods(http.MethodGet)
	r.HandleFunc("/ssai/session/{sessionId}/track", h.TrackAdEvent).Methods(http.MethodPost)
	r.HandleFunc("/ssai/playback/{videoId}", h.GetPlaybackURL).Methods(http.MethodGet)
	r.HandleFunc("/ssai/avails/{videoId}", h.GetAvailableSlots).Methods(http.MethodGet)
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

	params := ssai.SessionParams{
		PlayerParams: req.PlayerParams,
		AdParams:     req.AdParams,
		ViewerID:     req.ViewerID,
		DeviceType:   req.DeviceType,
	}

	session, err := h.mediaTailor.CreateSession(ctx, contentPath, params)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to create session", "error", err)
		span.RecordError(err)
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	response := CreateSessionResponse{
		SessionID:        session.SessionID,
		HLSManifestURL:   h.mediaTailor.GetPlaybackURL(session),
		DASHManifestURL:  h.mediaTailor.GetDASHPlaybackURL(session),
		TrackingEndpoint: session.TrackingEndpoint,
		ExpiresAt:        session.ExpiresAt,
		PlayerParams:     session.PlayerParams,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetSession retrieves session information.
func (h *AdsHandler) GetSession(w http.ResponseWriter, r *http.Request) {
	ctx, span := adsTracer.Start(r.Context(), "get-ssai-session")
	defer span.End()

	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

	if sessionID == "" {
		http.Error(w, "sessionId required", http.StatusBadRequest)
		return
	}

	span.SetAttributes(attribute.String("session.id", sessionID))

	// In a real implementation, you'd retrieve the session from a store
	// For now, return a minimal response
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

	vars := mux.Vars(r)
	sessionID := vars["sessionId"]

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

	// Create a minimal session object for tracking
	session := &ssai.Session{
		SessionID: sessionID,
	}

	if err := h.mediaTailor.ReportAdTracking(ctx, session, req.EventType, req.AdID); err != nil {
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

	vars := mux.Vars(r)
	videoID := vars["videoId"]

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

	params := ssai.SessionParams{
		AdParams: adParams,
	}

	contentPath := videoID + "/master.m3u8"
	session, err := h.mediaTailor.CreateSession(ctx, contentPath, params)
	if err != nil {
		h.logger.ErrorContext(ctx, "Failed to create playback session", "error", err)
		span.RecordError(err)
		http.Error(w, "Failed to get playback URL", http.StatusInternalServerError)
		return
	}

	response := GetPlaybackURLResponse{
		HLSManifestURL:  h.mediaTailor.GetPlaybackURL(session),
		DASHManifestURL: h.mediaTailor.GetDASHPlaybackURL(session),
		SessionID:       session.SessionID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetAvailableSlotsResponse is the response for available ad slots.
type GetAvailableSlotsResponse struct {
	VideoID  string           `json:"videoId"`
	Duration float64          `json:"duration"`
	Slots    []AdSlotResponse `json:"slots"`
}

// AdSlotResponse represents an ad slot in the response.
type AdSlotResponse struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"`
	StartTime   float64 `json:"startTime"`
	MaxDuration float64 `json:"maxDuration"`
}

// GetAvailableSlots returns available ad slots for a video.
func (h *AdsHandler) GetAvailableSlots(w http.ResponseWriter, r *http.Request) {
	ctx, span := adsTracer.Start(r.Context(), "get-available-slots")
	defer span.End()

	vars := mux.Vars(r)
	videoID := vars["videoId"]

	if videoID == "" {
		http.Error(w, "videoId required", http.StatusBadRequest)
		return
	}

	// Get content duration from query (or would be looked up from metadata)
	durationStr := r.URL.Query().Get("duration")
	duration := 3600.0 // Default 1 hour
	if durationStr != "" {
		if d, err := strconv.ParseFloat(durationStr, 64); err == nil {
			duration = d
		}
	}

	span.SetAttributes(
		attribute.String("video.id", videoID),
		attribute.Float64("duration", duration),
	)

	contentDuration := time.Duration(duration * float64(time.Second))

	// Define midroll positions (every 10 minutes)
	var midrollPositions []time.Duration
	for t := 10 * time.Minute; t < contentDuration-time.Minute; t += 10 * time.Minute {
		midrollPositions = append(midrollPositions, t)
	}

	markers := h.mediaTailor.GetAvailableSlots(ctx, contentDuration, midrollPositions)

	slots := make([]AdSlotResponse, 0, len(markers))
	for _, marker := range markers {
		slots = append(slots, AdSlotResponse{
			ID:          marker.ID,
			Type:        string(marker.Type),
			StartTime:   marker.Time.Seconds(),
			MaxDuration: marker.Duration.Seconds(),
		})
	}

	response := GetAvailableSlotsResponse{
		VideoID:  videoID,
		Duration: duration,
		Slots:    slots,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
