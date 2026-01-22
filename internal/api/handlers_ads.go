package api

// AdBreakRequest represents a request to insert an ad break
type AdBreakRequest struct {
    VideoID      string  `json:"videoId"`
    PositionSec  float64 `json:"positionSec"`
    DurationSec  float64 `json:"durationSec"`
    AdPodID      string  `json:"adPodId,omitempty"`
}

// GetPersonalizedManifestHandler returns a manifest URL with SSAI
func (h *Handlers) GetPersonalizedManifestHandler(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    videoID := r.URL.Query().Get("videoId")
    
    // Get viewer parameters for ad targeting
    viewerParams := map[string]string{
        "deviceType": r.Header.Get("X-Device-Type"),
        "country":    r.Header.Get("CF-IPCountry"),
        "sessionId":  r.Header.Get("X-Session-ID"),
    }
    
    // Generate MediaTailor session URL
    sessionURL := h.generateMediaTailorSession(ctx, videoID, viewerParams)
    
    h.writeJSON(ctx, w, http.StatusOK, map[string]string{
        "manifestUrl": sessionURL,
        "trackingUrl": fmt.Sprintf("%s/v1/tracking", h.cfg.MediaTailorEndpoint),
    })
}
