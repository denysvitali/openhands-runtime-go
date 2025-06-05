package telemetry

import (
	"context"
	"time"

	"github.com/denysvitali/openhands-runtime-go/pkg/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

// Initialize sets up OpenTelemetry tracing
func Initialize(cfg config.TelemetryConfig) (func(), error) {
	// Create resource
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("openhands-runtime"),
			semconv.ServiceVersionKey.String("1.0.0"),
		),
	)
	if err != nil {
		return nil, err
	}

	// Create exporter
	var exporter sdktrace.SpanExporter
	if cfg.Endpoint != "" {
		// Use specified endpoint
		exporter, err = otlptracehttp.New(
			context.Background(),
			otlptracehttp.WithEndpoint(cfg.Endpoint),
		)
	} else {
		// Use auto-export (environment variables)
		exporter, err = otlptracehttp.New(context.Background())
	}
	if err != nil {
		return nil, err
	}

	// Create trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Set global trace provider
	otel.SetTracerProvider(tp)

	// Set global propagator
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Return cleanup function
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			// Log error but don't fail
		}
	}, nil
}
