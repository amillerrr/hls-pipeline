package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// SRTConfig contains SRT-specific configuration.
type SRTConfig struct {
	// ListenPort is the port to listen for SRT connections
	ListenPort int `json:"listenPort"`

	// Latency is the SRT latency in milliseconds
	Latency int `json:"latency"`

	// Passphrase for SRT encryption (empty = no encryption)
	Passphrase string `json:"-"`

	// PayloadSize is the SRT payload size
	PayloadSize int `json:"payloadSize"`

	// MaxBW is the maximum bandwidth in bytes/sec (0 = unlimited)
	MaxBW int64 `json:"maxBw"`

	// Mode: "listener", "caller", or "rendezvous"
	Mode string `json:"mode"`

	// StreamID for caller mode
	StreamID string `json:"streamId,omitempty"`

	// ConnectTimeout for caller mode (ms)
	ConnectTimeout int `json:"connectTimeout,omitempty"`
}

// DefaultSRTConfig returns default SRT configuration.
func DefaultSRTConfig() *SRTConfig {
	return &SRTConfig{
		ListenPort:  9000,
		Latency:     200,  // 200ms default latency
		PayloadSize: 1316, // Standard SRT payload
		Mode:        "listener",
	}
}

// SRTIngestHandler handles SRT ingest streams.
type SRTIngestHandler struct {
	config        *SRTConfig
	streamManager *StreamManager
	logger        *slog.Logger
	mediamtx      *MediaMTXClient
	
	// Port allocation
	portMu        sync.Mutex
	allocatedPorts map[int]string // port -> streamID
	portRange     [2]int          // [start, end]
}

// NewSRTIngestHandler creates a new SRT ingest handler.
func NewSRTIngestHandler(
	config *SRTConfig,
	streamManager *StreamManager,
	mediamtxClient *MediaMTXClient,
	logger *slog.Logger,
) *SRTIngestHandler {
	if config == nil {
		config = DefaultSRTConfig()
	}

	return &SRTIngestHandler{
		config:         config,
		streamManager:  streamManager,
		logger:         logger,
		mediamtx:       mediamtxClient,
		allocatedPorts: make(map[int]string),
		portRange:      [2]int{9000, 9100}, // Default port range
	}
}

// CreateSRTEndpoint creates a new SRT ingest endpoint.
func (h *SRTIngestHandler) CreateSRTEndpoint(ctx context.Context, streamName string, cfg *StreamConfig) (*SRTEndpoint, error) {
	// Allocate a port
	port, err := h.allocatePort()
	if err != nil {
		return nil, err
	}

	// Configure SRT-specific settings
	if cfg.InputPort == 0 {
		cfg.InputPort = port
	}
	if cfg.InputMode == "" {
		cfg.InputMode = h.config.Mode
	}
	if cfg.InputPassphrase == "" {
		cfg.InputPassphrase = h.config.Passphrase
	}

	// Create the stream
	stream, err := h.streamManager.CreateStream(ctx, streamName, InputTypeSRT, cfg)
	if err != nil {
		h.releasePort(port)
		return nil, err
	}

	// Register with MediaMTX if available
	if h.mediamtx != nil {
		if err := h.mediamtx.AddPath(ctx, stream.ID, &MediaMTXPathConfig{
			Source:         fmt.Sprintf("srt://localhost:%d", port),
			SourceProtocol: "srt",
		}); err != nil {
			h.logger.Warn("failed to register path with mediamtx",
				"streamId", stream.ID,
				"error", err,
			)
		}
	}

	// Build the SRT URL for publishers
	srtURL := h.buildSRTURL(port, stream.ID)

	endpoint := &SRTEndpoint{
		StreamID:   stream.ID,
		StreamName: streamName,
		Port:       port,
		SRTURL:     srtURL,
		Status:     "ready",
		CreatedAt:  time.Now(),
		Stream:     stream,
	}

	h.logger.Info("SRT endpoint created",
		"streamId", stream.ID,
		"port", port,
		"srtUrl", srtURL,
	)

	return endpoint, nil
}

// StartSRTEndpoint starts an SRT endpoint for receiving streams.
func (h *SRTIngestHandler) StartSRTEndpoint(ctx context.Context, streamID string) error {
	return h.streamManager.StartStream(ctx, streamID)
}

// StopSRTEndpoint stops an SRT endpoint.
func (h *SRTIngestHandler) StopSRTEndpoint(ctx context.Context, streamID string) error {
	stream, err := h.streamManager.GetStream(streamID)
	if err != nil {
		return err
	}

	// Release the port
	h.releasePort(stream.Config.InputPort)

	// Remove from MediaMTX
	if h.mediamtx != nil {
		h.mediamtx.RemovePath(ctx, streamID)
	}

	return h.streamManager.StopStream(ctx, streamID)
}

// allocatePort allocates an available port from the port range.
func (h *SRTIngestHandler) allocatePort() (int, error) {
	h.portMu.Lock()
	defer h.portMu.Unlock()

	for port := h.portRange[0]; port <= h.portRange[1]; port++ {
		if _, used := h.allocatedPorts[port]; !used {
			h.allocatedPorts[port] = ""
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available ports in range %d-%d", h.portRange[0], h.portRange[1])
}

// releasePort releases an allocated port.
func (h *SRTIngestHandler) releasePort(port int) {
	h.portMu.Lock()
	defer h.portMu.Unlock()
	delete(h.allocatedPorts, port)
}

// buildSRTURL builds the SRT URL for publishers.
func (h *SRTIngestHandler) buildSRTURL(port int, streamID string) string {
	url := fmt.Sprintf("srt://0.0.0.0:%d?mode=caller&latency=%d",
		port,
		h.config.Latency,
	)

	if streamID != "" {
		url += "&streamid=" + streamID
	}

	return url
}

// SRTEndpoint represents an SRT ingest endpoint.
type SRTEndpoint struct {
	StreamID   string    `json:"streamId"`
	StreamName string    `json:"streamName"`
	Port       int       `json:"port"`
	SRTURL     string    `json:"srtUrl"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
	Stream     *Stream   `json:"-"`
}

// MediaMTXClient communicates with the MediaMTX API.
type MediaMTXClient struct {
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
}

// MediaMTXConfig contains MediaMTX client configuration.
type MediaMTXConfig struct {
	BaseURL string `json:"baseUrl"`
	APIPort int    `json:"apiPort"`
}

// NewMediaMTXClient creates a new MediaMTX client.
func NewMediaMTXClient(cfg *MediaMTXConfig, logger *slog.Logger) *MediaMTXClient {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://localhost:%d", cfg.APIPort)
	}

	return &MediaMTXClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// MediaMTXPathConfig contains path configuration for MediaMTX.
type MediaMTXPathConfig struct {
	Source             string `json:"source,omitempty"`
	SourceProtocol     string `json:"sourceProtocol,omitempty"`
	SourceOnDemand     bool   `json:"sourceOnDemand,omitempty"`
	SourceFingerprint  string `json:"sourceFingerprint,omitempty"`
	MaxReaders         int    `json:"maxReaders,omitempty"`
	Record             bool   `json:"record,omitempty"`
	RecordPath         string `json:"recordPath,omitempty"`
	RecordFormat       string `json:"recordFormat,omitempty"`
	Fallback           string `json:"fallback,omitempty"`
}

// AddPath adds a new path to MediaMTX.
func (c *MediaMTXClient) AddPath(ctx context.Context, name string, cfg *MediaMTXPathConfig) error {
	url := fmt.Sprintf("%s/v3/config/paths/add/%s", c.baseURL, name)
	return c.postJSON(ctx, url, cfg)
}

// RemovePath removes a path from MediaMTX.
func (c *MediaMTXClient) RemovePath(ctx context.Context, name string) error {
	url := fmt.Sprintf("%s/v3/config/paths/delete/%s", c.baseURL, name)
	return c.delete(ctx, url)
}

// GetPath gets path information from MediaMTX.
func (c *MediaMTXClient) GetPath(ctx context.Context, name string) (*MediaMTXPath, error) {
	url := fmt.Sprintf("%s/v3/paths/get/%s", c.baseURL, name)
	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var path MediaMTXPath
	if err := json.NewDecoder(resp.Body).Decode(&path); err != nil {
		return nil, err
	}

	return &path, nil
}

// ListPaths lists all paths in MediaMTX.
func (c *MediaMTXClient) ListPaths(ctx context.Context) (*MediaMTXPathList, error) {
	url := fmt.Sprintf("%s/v3/paths/list", c.baseURL)
	
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var list MediaMTXPathList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}

	return &list, nil
}

func (c *MediaMTXClient) postJSON(ctx context.Context, url string, body interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, 
		io.NopCloser(jsonReader(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed: %d - %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *MediaMTXClient) delete(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("delete failed: %d", resp.StatusCode)
	}

	return nil
}

// MediaMTXPath represents a path in MediaMTX.
type MediaMTXPath struct {
	Name          string            `json:"name"`
	ConfName      string            `json:"confName"`
	Source        *MediaMTXSource   `json:"source,omitempty"`
	Ready         bool              `json:"ready"`
	ReadyTime     *time.Time        `json:"readyTime,omitempty"`
	Tracks        []string          `json:"tracks,omitempty"`
	BytesReceived int64             `json:"bytesReceived"`
	BytesSent     int64             `json:"bytesSent"`
	Readers       []MediaMTXReader  `json:"readers,omitempty"`
}

// MediaMTXSource represents a source in MediaMTX.
type MediaMTXSource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// MediaMTXReader represents a reader in MediaMTX.
type MediaMTXReader struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// MediaMTXPathList contains a list of paths.
type MediaMTXPathList struct {
	ItemCount int            `json:"itemCount"`
	PageCount int            `json:"pageCount"`
	Items     []MediaMTXPath `json:"items"`
}

// jsonReader creates a reader from JSON bytes.
func jsonReader(data []byte) io.Reader {
	return &jsonBytesReader{data: data}
}

type jsonBytesReader struct {
	data []byte
	pos  int
}

func (r *jsonBytesReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

