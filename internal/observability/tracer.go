package observability

import (
	"context"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// Create a new trace provider instance
func InitTracer(ctx context.Context, serviceName string) func(context.Context) error {
	// Create the Exporter
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint("localhost:4317"),
	)
	if err != nil {
		slog.Error("Failed to create trace exporter", "error", err)
		os.Exit(1)
	}

	// Create the Resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.DeploymentEnvironment("dev"),
		),
	)
	if err != nil {
		slog.Error("Failed to create trace resource", "error", err)
		os.Exit(1)
	}

	// Register the Trace Provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	slog.Info("OpenTelemetry Tracer initialized", "service", serviceName)

	return tp.Shutdown
}
