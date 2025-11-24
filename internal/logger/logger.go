package logger

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

func New() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// Log Trace ID and Span ID
func Info(ctx context.Context, logger *slog.Logger, msg string, args ...any) {
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		args = append(args,
			"trace_id", spanCtx.TraceID().String(),
			"span_id", spanCtx.SpanID().String(),
		)
	}
	logger.Info(msg, args...)
}

// Log an errors with Trace ID
func Error(ctx context.Context, logger *slog.Logger, msg string, args ...any) {
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		args = append(args,
			"trace_id", spanCtx.TraceID().String(),
			"span_id", spanCtx.SpanID().String(),
		)
	}
	logger.Error(msg, args...)
}
