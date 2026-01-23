package live

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Stream defines the metadata for a live stream.
type Stream struct {
	ID        string        `json:"id" dynamodbav:"id"`
	Name      string        `json:"name" dynamodbav:"name"`
	State     StreamState   `json:"state" dynamodbav:"state"`
	InputType InputType     `json:"inputType" dynamodbav:"inputType"`
	InputURL  string        `json:"inputUrl" dynamodbav:"inputUrl"`
	OutputURL string        `json:"outputUrl,omitempty" dynamodbav:"outputUrl,omitempty"`
	Config    *StreamConfig `json:"config" dynamodbav:"config"`
	CreatedAt time.Time     `json:"createdAt" dynamodbav:"createdAt"`
}

// ManagerConfig holds configuration for the stream manager.
type ManagerConfig struct {
	OutputBucket string
	OutputPrefix string
	AWSRegion    string
	CDNDomain    string
}

// StreamManager manages live stream lifecycles.
type StreamManager struct {
	store  StreamStore
	logger *slog.Logger
	config *ManagerConfig

	// Local active processes (not persisted)
	mu              sync.Mutex
	activeProcesses map[string]*LivePackager
}

func NewStreamManager(store StreamStore, cfg *ManagerConfig, logger *slog.Logger) *StreamManager {
	return &StreamManager{
		store:           store,
		config:          cfg,
		logger:          logger,
		activeProcesses: make(map[string]*LivePackager),
	}
}

// CreateStream creates a new stream definition in the store.
func (m *StreamManager) CreateStream(ctx context.Context, name string, inputType InputType, cfg *StreamConfig) (*Stream, error) {
	id := uuid.New().String()[:8]
	
	// Set defaults
	if cfg.OutputBucket == "" { cfg.OutputBucket = m.config.OutputBucket }
	if cfg.OutputPrefix == "" { cfg.OutputPrefix = fmt.Sprintf("live/%s", id) }

	stream := &Stream{
		ID:        id,
		Name:      name,
		State:     StreamStateIdle,
		InputType: inputType,
		Config:    cfg,
		CreatedAt: time.Now(),
		InputURL:  buildInputURL(inputType, cfg, m.config), // Helper from previous implementation
	}

	if err := m.store.SaveStream(ctx, stream); err != nil {
		return nil, err
	}
	return stream, nil
}

// StartStream launches the ingest/packaging process on this instance.
func (m *StreamManager) StartStream(ctx context.Context, id string) error {
	stream, err := m.store.GetStream(ctx, id)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if _, exists := m.activeProcesses[id]; exists {
		m.mu.Unlock()
		return fmt.Errorf("stream already active on this node")
	}
	m.mu.Unlock()

	// Update state to Starting
	if err := m.store.UpdateState(ctx, id, StreamStateStarting, ""); err != nil {
		return err
	}

	// Initialize Packager (uses the unified pipeline logic internally)
	packager, err := NewLivePackager(&LivePackagerConfig{
		StreamID:     id,
		InputURL:     stream.InputURL,
		OutputBucket: stream.Config.OutputBucket,
		OutputPrefix: stream.Config.OutputPrefix,
		AWSRegion:    m.config.AWSRegion,
	}, m.logger)
	if err != nil {
		return err
	}

	// Run in background
	go func() {
		m.mu.Lock()
		m.activeProcesses[id] = packager
		m.mu.Unlock()

		outputURL := buildOutputURL(stream.Config, m.config) // Helper
		_ = m.store.UpdateState(context.Background(), id, StreamStateActive, outputURL)

		// This blocks until stream stops or fails
		err := packager.Start(context.Background()) 
		
		// Cleanup
		m.mu.Lock()
		delete(m.activeProcesses, id)
		m.mu.Unlock()

		newState := StreamStateStopped
		if err != nil {
			m.logger.Error("stream failed", "id", id, "error", err)
			newState = StreamStateError
		}
		_ = m.store.UpdateState(context.Background(), id, newState, "")
	}()

	return nil
}

// StopStream terminates the local process.
func (m *StreamManager) StopStream(ctx context.Context, id string) error {
	m.mu.Lock()
	packager, exists := m.activeProcesses[id]
	m.mu.Unlock()

	if !exists {
		// If not local, we might want to just update DB state, 
		// but in this simplified version we assume single-instance or sticky sessions.
		return fmt.Errorf("stream not active on this node")
	}

	return packager.Stop(ctx)
}

func (m *StreamManager) ListStreams(ctx context.Context) ([]*Stream, error) {
	return m.store.ListStreams(ctx)
}

func (m *StreamManager) GetStream(id string) (*Stream, error) {
	return m.store.GetStream(context.Background(), id)
}

func (m *StreamManager) DeleteStream(ctx context.Context, id string) error {
	_ = m.StopStream(ctx, id)
	return m.store.DeleteStream(ctx, id)
}
