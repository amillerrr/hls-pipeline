package observability

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

// Config contains observability configuration.
type Config struct {
	// ServiceName is the name of the service.
	ServiceName string

	// ServiceVersion is the version of the service.
	ServiceVersion string

	// Environment is the deployment environment.
	Environment string

	// OTLPEndpoint is the OpenTelemetry collector endpoint.
	OTLPEndpoint string

	// EnableConsoleExporter enables console trace export (for development).
	EnableConsoleExporter bool

	// SamplingRate is the trace sampling rate (0.0-1.0).
	SamplingRate float64

	// LogLevel is the minimum log level.
	LogLevel slog.Level
}

// DefaultConfig returns the default observability configuration.
func DefaultConfig() *Config {
	return &Config{
		ServiceName:           "hls-pipeline",
		ServiceVersion:        "1.0.0",
		Environment:           getEnvOrDefault("ENVIRONMENT", "development"),
		OTLPEndpoint:          getEnvOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		EnableConsoleExporter: getEnvOrDefault("ENVIRONMENT", "development") == "development",
		SamplingRate:          1.0,
		LogLevel:              slog.LevelInfo,
	}
}

// Provider manages observability resources.
type Provider struct {
	config         *Config
	tracerProvider *sdktrace.TracerProvider
	logger         *slog.Logger
}

// NewProvider creates a new observability provider.
func NewProvider(config *Config) (*Provider, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// Create resource
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(config.ServiceName),
			semconv.ServiceVersion(config.ServiceVersion),
			semconv.DeploymentEnvironment(config.Environment),
		),
	)
	if err != nil {
		return nil, err
	}

	// Create trace exporter
	var exporter sdktrace.SpanExporter
	if config.OTLPEndpoint != "" {
		client := otlptracegrpc.NewClient(
			otlptracegrpc.WithEndpoint(config.OTLPEndpoint),
			otlptracegrpc.WithInsecure(),
		)
		exporter, err = otlptrace.New(context.Background(), client)
		if err != nil {
			return nil, err
		}
	} else if config.EnableConsoleExporter {
		exporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, err
		}
	}

	// Create sampler
	var sampler sdktrace.Sampler
	if config.SamplingRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if config.SamplingRate <= 0.0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.TraceIDRatioBased(config.SamplingRate)
	}

	// Create tracer provider
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	}
	if exporter != nil {
		opts = append(opts, sdktrace.WithBatcher(exporter))
	}

	tp := sdktrace.NewTracerProvider(opts...)

	// Set global tracer provider
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Create logger
	logHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: config.LogLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Add trace context to logs
			if a.Key == slog.TimeKey {
				return slog.Attr{Key: "timestamp", Value: a.Value}
			}
			return a
		},
	})
	logger := slog.New(logHandler)

	return &Provider{
		config:         config,
		tracerProvider: tp,
		logger:         logger,
	}, nil
}

// Tracer returns a tracer for the given name.
func (p *Provider) Tracer(name string) trace.Tracer {
	return p.tracerProvider.Tracer(name)
}

// Logger returns the configured logger.
func (p *Provider) Logger() *slog.Logger {
	return p.logger
}

// Shutdown shuts down the observability provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.tracerProvider != nil {
		return p.tracerProvider.Shutdown(ctx)
	}
	return nil
}

// TracingHandler provides HTTP middleware for tracing and logging.
// This replaces the redundant logger package.
type TracingHandler struct {
	tracer trace.Tracer
	logger *slog.Logger
}

// NewTracingHandler creates a new tracing handler.
func NewTracingHandler(tracer trace.Tracer, logger *slog.Logger) *TracingHandler {
	return &TracingHandler{
		tracer: tracer,
		logger: logger,
	}
}

// Middleware returns HTTP middleware that adds tracing and logging.
func (h *TracingHandler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()

		// Extract trace context from headers
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		// Start span
		ctx, span := h.tracer.Start(ctx, r.Method+" "+r.URL.Path,
			trace.WithAttributes(
				semconv.HTTPMethod(r.Method),
				semconv.HTTPURL(r.URL.String()),
				semconv.HTTPUserAgent(r.UserAgent()),
				semconv.NetHostName(r.Host),
			),
			trace.WithSpanKind(trace.SpanKindServer),
		)
		defer span.End()

		// Create response writer wrapper to capture status code
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Add request ID to context
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = span.SpanContext().TraceID().String()
		}
		ctx = context.WithValue(ctx, requestIDKey, requestID)

		// Set request ID header
		w.Header().Set("X-Request-ID", requestID)

		// Log request
		h.logger.InfoContext(ctx, "Request started",
			"method", r.Method,
			"path", r.URL.Path,
			"requestId", requestID,
			"remoteAddr", r.RemoteAddr,
		)

		// Call next handler
		next.ServeHTTP(wrapped, r.WithContext(ctx))

		// Record response attributes
		duration := time.Since(startTime)
		span.SetAttributes(
			semconv.HTTPStatusCode(wrapped.statusCode),
			attribute.Int64("http.response_size", int64(wrapped.bytesWritten)),
			attribute.Int64("http.duration_ms", duration.Milliseconds()),
		)

		// Log response
		logLevel := slog.LevelInfo
		if wrapped.statusCode >= 500 {
			logLevel = slog.LevelError
		} else if wrapped.statusCode >= 400 {
			logLevel = slog.LevelWarn
		}

		h.logger.Log(ctx, logLevel, "Request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration_ms", duration.Milliseconds(),
			"bytes", wrapped.bytesWritten,
			"requestId", requestID,
		)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code and bytes written.
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

// Flush implements http.Flusher.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Context key for request ID.
type contextKey string

const requestIDKey contextKey = "requestID"

// GetRequestID returns the request ID from the context.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// LoggerWithTrace returns a logger with trace context attributes.
func LoggerWithTrace(logger *slog.Logger, ctx context.Context) *slog.Logger {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return logger
	}

	return logger.With(
		"traceId", span.SpanContext().TraceID().String(),
		"spanId", span.SpanContext().SpanID().String(),
	)
}

// Helper functions

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// RecordError records an error on the current span.
func RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
}

// AddEvent adds an event to the current span.
func AddEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

// SetAttributes sets attributes on the current span.
func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attrs...)
}
