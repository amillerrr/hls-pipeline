package transcoder

import (
	"context"
	"github.com/amillerrr/hls-pipeline/internal/config"
)

// PipelineInterface defines the unified processing contract.
type PipelineInterface interface {
	Process(ctx context.Context, input JobInput) error
}

// JobInput defines the parameters for a transcoding job
type JobInput struct {
	InputPath  string
	OutputDir  string
	StreamID   string
	Presets    []config.PresetConfig
}

// Ensure Pipeline satisfies the interface
var _ PipelineInterface = (*Pipeline)(nil)
