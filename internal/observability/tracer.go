package observability

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/amillerrr/hls-pipeline/internal/config"
)

// TracingHandler wraps an slog.Handler to add trace context to log records.
type TracingHandler struct {
	slog.Handler
}

// NewTracingHandler creates a new TracingHandler wrapping the given handler.
func NewTracingHandler(h slog.Handler) *TracingHandler {
	return &TracingHandler{Handler: h}
}

// Handle adds trace_id and span_id to log records if available in context.
func (h *TracingHandler) Handle(ctx context.Context, r slog.Record) error {
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", spanCtx.TraceID().String()),
			slog.String("span_id", spanCtx.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs returns a new TracingHandler with the given attributes.
func (h *TracingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TracingHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup returns a new TracingHandler with the given group.
func (h *TracingHandler) WithGroup(name string) slog.Handler {
	return &TracingHandler{Handler: h.Handler.WithGroup(name)}
}

// NewLogger creates a new logger with trace context support.
func NewLogger() *slog.Logger {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	return slog.New(NewTracingHandler(jsonHandler))
}

// NewLoggerWithLevel creates a new logger with the specified level.
func NewLoggerWithLevel(level slog.Level) *slog.Logger {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(NewTracingHandler(jsonHandler))
}

// InitTracer creates a new OpenTelemetry trace provider.
func InitTracer(ctx context.Context, serviceName string, cfg *config.Config) (func(context.Context) error, error) {
	endpoint := cfg.Observability.OTLPEndpoint
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	// Remove protocol prefix if present
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	// Create the exporter
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(endpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	// Create the resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace resource: %w", err)
	}

	// Create and register the trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	slog.Info("OpenTelemetry tracer initialized", "service", serviceName, "endpoint", endpoint)

	return tp.Shutdown, nil
}

// InitTracerSimple creates a trace provider using environment variables.
func InitTracerSimple(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:4317"
	}

	env := os.Getenv("ENV")
	if env == "" {
		env = "dev"
	}

	cfg := &config.Config{
		Environment: env,
		Observability: config.ObservabilityConfig{
			OTLPEndpoint: endpoint,
		},
	}

	return InitTracer(ctx, serviceName, cfg)
}
