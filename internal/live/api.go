package live

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// APIHandler handles HTTP requests for live streaming.
type APIHandler struct {
	streamManager *StreamManager
	srtHandler    *SRTIngestHandler
	logger        *slog.Logger
}

// NewAPIHandler creates a new API handler.
func NewAPIHandler(
	streamManager *StreamManager,
	srtHandler *SRTIngestHandler,
	logger *slog.Logger,
) *APIHandler {
	return &APIHandler{
		streamManager: streamManager,
		srtHandler:    srtHandler,
		logger:        logger,
	}
}

// RegisterRoutes registers the live streaming routes.
func (h *APIHandler) RegisterRoutes(r chi.Router) {
	r.Route("/live", func(r chi.Router) {
		// Stream management
		r.Get("/streams", h.ListStreams)
		r.Post("/streams", h.CreateStream)
		r.Get("/streams/{streamId}", h.GetStream)
		r.Delete("/streams/{streamId}", h.DeleteStream)
		
		// Stream control
		r.Post("/streams/{streamId}/start", h.StartStream)
		r.Post("/streams/{streamId}/stop", h.StopStream)
		
		// SRT-specific endpoints
		r.Post("/srt/endpoints", h.CreateSRTEndpoint)
		r.Get("/srt/endpoints/{streamId}", h.GetSRTEndpoint)
	})
}

// CreateStreamRequest is the request body for creating a stream.
type CreateStreamRequest struct {
	Name            string   `json:"name" validate:"required"`
	InputType       string   `json:"inputType" validate:"required,oneof=srt rtmp rtsp udp"`
	InputPort       int      `json:"inputPort,omitempty"`
	InputPath       string   `json:"inputPath,omitempty"`
	InputMode       string   `json:"inputMode,omitempty"`
	OutputBucket    string   `json:"outputBucket,omitempty"`
	OutputPrefix    string   `json:"outputPrefix,omitempty"`
	Presets         []string `json:"presets,omitempty"`
	EnableLLHLS     bool     `json:"enableLlhls,omitempty"`
	EnableDRM       bool     `json:"enableDrm,omitempty"`
	EnableSSAI      bool     `json:"enableSsai,omitempty"`
	DVRWindowSize   int      `json:"dvrWindowSize,omitempty"`
	EnableRecording bool     `json:"enableRecording,omitempty"`
}

// StreamResponse is the response for stream operations.
type StreamResponse struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	State       StreamState       `json:"state"`
	InputType   InputType         `json:"inputType"`
	InputURL    string            `json:"inputUrl"`
	OutputURL   string            `json:"outputUrl,omitempty"`
	StartedAt   *string           `json:"startedAt,omitempty"`
	StoppedAt   *string           `json:"stoppedAt,omitempty"`
	Error       string            `json:"error,omitempty"`
	Stats       *StreamStats      `json:"stats,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// CreateStream creates a new live stream.
func (h *APIHandler) CreateStream(w http.ResponseWriter, r *http.Request) {
	var req CreateStreamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	inputType := InputType(req.InputType)
	if inputType == "" {
		inputType = InputTypeSRT
	}

	config := &StreamConfig{
		InputPort:       req.InputPort,
		InputPath:       req.InputPath,
		InputMode:       req.InputMode,
		OutputBucket:    req.OutputBucket,
		OutputPrefix:    req.OutputPrefix,
		Presets:         req.Presets,
		EnableLLHLS:     req.EnableLLHLS,
		EnableDRM:       req.EnableDRM,
		EnableSSAI:      req.EnableSSAI,
		DVRWindowSize:   req.DVRWindowSize,
		EnableRecording: req.EnableRecording,
	}

	stream, err := h.streamManager.CreateStream(r.Context(), req.Name, inputType, config)
	if err != nil {
		h.logger.Error("failed to create stream", "error", err)
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.writeJSON(w, http.StatusCreated, h.streamToResponse(stream))
}

// ListStreams lists all live streams.
func (h *APIHandler) ListStreams(w http.ResponseWriter, r *http.Request) {
	streams := h.streamManager.ListStreams()

	responses := make([]StreamResponse, len(streams))
	for i, stream := range streams {
		responses[i] = h.streamToResponse(stream)
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"streams": responses,
		"count":   len(responses),
	})
}

// GetStream gets a specific stream.
func (h *APIHandler) GetStream(w http.ResponseWriter, r *http.Request) {
	streamID := chi.URLParam(r, "streamId")
	
	stream, err := h.streamManager.GetStream(streamID)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "stream not found")
		return
	}

	h.writeJSON(w, http.StatusOK, h.streamToResponse(stream))
}

// DeleteStream deletes a stream.
func (h *APIHandler) DeleteStream(w http.ResponseWriter, r *http.Request) {
	streamID := chi.URLParam(r, "streamId")
	
	if err := h.streamManager.DeleteStream(r.Context(), streamID); err != nil {
		h.logger.Error("failed to delete stream", "streamId", streamID, "error", err)
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// StartStream starts a stream.
func (h *APIHandler) StartStream(w http.ResponseWriter, r *http.Request) {
	streamID := chi.URLParam(r, "streamId")
	
	if err := h.streamManager.StartStream(r.Context(), streamID); err != nil {
		h.logger.Error("failed to start stream", "streamId", streamID, "error", err)
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	stream, _ := h.streamManager.GetStream(streamID)
	h.writeJSON(w, http.StatusOK, h.streamToResponse(stream))
}

// StopStream stops a stream.
func (h *APIHandler) StopStream(w http.ResponseWriter, r *http.Request) {
	streamID := chi.URLParam(r, "streamId")
	
	if err := h.streamManager.StopStream(r.Context(), streamID); err != nil {
		h.logger.Error("failed to stop stream", "streamId", streamID, "error", err)
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	stream, _ := h.streamManager.GetStream(streamID)
	h.writeJSON(w, http.StatusOK, h.streamToResponse(stream))
}

// CreateSRTEndpointRequest is the request for creating an SRT endpoint.
type CreateSRTEndpointRequest struct {
	Name            string   `json:"name" validate:"required"`
	OutputBucket    string   `json:"outputBucket,omitempty"`
	OutputPrefix    string   `json:"outputPrefix,omitempty"`
	Presets         []string `json:"presets,omitempty"`
	EnableLLHLS     bool     `json:"enableLlhls,omitempty"`
	DVRWindowSize   int      `json:"dvrWindowSize,omitempty"`
	Passphrase      string   `json:"passphrase,omitempty"`
	AutoStart       bool     `json:"autoStart,omitempty"`
}

// SRTEndpointResponse is the response for SRT endpoint operations.
type SRTEndpointResponse struct {
	StreamID   string    `json:"streamId"`
	StreamName string    `json:"streamName"`
	Port       int       `json:"port"`
	SRTURL     string    `json:"srtUrl"`
	Status     string    `json:"status"`
	OutputURL  string    `json:"outputUrl,omitempty"`
}

// CreateSRTEndpoint creates a new SRT ingest endpoint.
func (h *APIHandler) CreateSRTEndpoint(w http.ResponseWriter, r *http.Request) {
	if h.srtHandler == nil {
		h.writeError(w, http.StatusServiceUnavailable, "SRT ingest not available")
		return
	}

	var req CreateSRTEndpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	config := &StreamConfig{
		OutputBucket:    req.OutputBucket,
		OutputPrefix:    req.OutputPrefix,
		Presets:         req.Presets,
		EnableLLHLS:     req.EnableLLHLS,
		DVRWindowSize:   req.DVRWindowSize,
		InputPassphrase: req.Passphrase,
	}

	endpoint, err := h.srtHandler.CreateSRTEndpoint(r.Context(), req.Name, config)
	if err != nil {
		h.logger.Error("failed to create SRT endpoint", "error", err)
		h.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Auto-start if requested
	if req.AutoStart {
		if err := h.srtHandler.StartSRTEndpoint(r.Context(), endpoint.StreamID); err != nil {
			h.logger.Warn("failed to auto-start SRT endpoint",
				"streamId", endpoint.StreamID,
				"error", err,
			)
		}
	}

	h.writeJSON(w, http.StatusCreated, SRTEndpointResponse{
		StreamID:   endpoint.StreamID,
		StreamName: endpoint.StreamName,
		Port:       endpoint.Port,
		SRTURL:     endpoint.SRTURL,
		Status:     endpoint.Status,
	})
}

// GetSRTEndpoint gets SRT endpoint information.
func (h *APIHandler) GetSRTEndpoint(w http.ResponseWriter, r *http.Request) {
	streamID := chi.URLParam(r, "streamId")
	
	stream, err := h.streamManager.GetStream(streamID)
	if err != nil {
		h.writeError(w, http.StatusNotFound, "endpoint not found")
		return
	}

	if stream.InputType != InputTypeSRT {
		h.writeError(w, http.StatusBadRequest, "stream is not an SRT endpoint")
		return
	}

	h.writeJSON(w, http.StatusOK, SRTEndpointResponse{
		StreamID:   stream.ID,
		StreamName: stream.Name,
		Port:       stream.Config.InputPort,
		SRTURL:     stream.InputURL,
		Status:     string(stream.State),
		OutputURL:  stream.OutputURL,
	})
}

// streamToResponse converts a Stream to a StreamResponse.
func (h *APIHandler) streamToResponse(stream *Stream) StreamResponse {
	resp := StreamResponse{
		ID:        stream.ID,
		Name:      stream.Name,
		State:     stream.State,
		InputType: stream.InputType,
		InputURL:  stream.InputURL,
		OutputURL: stream.OutputURL,
		Error:     stream.Error,
		Stats:     stream.Stats,
		Metadata:  stream.Metadata,
	}

	if stream.StartedAt != nil {
		t := stream.StartedAt.Format("2006-01-02T15:04:05Z")
		resp.StartedAt = &t
	}
	if stream.StoppedAt != nil {
		t := stream.StoppedAt.Format("2006-01-02T15:04:05Z")
		resp.StoppedAt = &t
	}

	return resp
}

func (h *APIHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *APIHandler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{
		"error": message,
	})
}

// HealthCheck returns the health status of the live service.
func (h *APIHandler) HealthCheck(ctx context.Context) map[string]interface{} {
	streams := h.streamManager.ListStreams()
	
	activeCount := 0
	for _, s := range streams {
		if s.State == StreamStateActive {
			activeCount++
		}
	}

	return map[string]interface{}{
		"status":         "healthy",
		"totalStreams":   len(streams),
		"activeStreams":  activeCount,
	}
}

