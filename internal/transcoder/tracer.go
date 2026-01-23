package transcoder

import "go.opentelemetry.io/otel"

// Single tracer instance for the entire transcoder package.
var tracer = otel.Tracer("hls-pipeline/transcoder")
