package main

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// initOTel initialises the OpenTelemetry SDK with OTLP gRPC exporters for
// both traces and metrics. The OTLP endpoint is read from the standard
// OTEL_EXPORTER_OTLP_ENDPOINT environment variable (defaults to
// localhost:4317 if unset).
//
// Returns a shutdown function that must be deferred to flush all telemetry
// before the process exits.
func initOTel(ctx context.Context, serviceName string) (shutdown func(context.Context) error, err error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTel resource: %w", err)
	}

	// --- Trace exporter ---
	traceExporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(traceExporter),
		trace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// --- Metric exporter ---
	metricExporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		// Clean up trace provider before returning
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExporter)),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	// Return a composite shutdown that flushes both providers.
	shutdown = func(ctx context.Context) error {
		var errs []error
		if err := tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("trace provider shutdown: %w", err))
		}
		if err := mp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter provider shutdown: %w", err))
		}
		if len(errs) > 0 {
			return fmt.Errorf("OTel shutdown errors: %v", errs)
		}
		return nil
	}

	return shutdown, nil
}
