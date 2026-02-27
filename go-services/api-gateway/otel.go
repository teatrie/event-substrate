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

// initOTel initialises the global OpenTelemetry TracerProvider and MeterProvider
// and returns a shutdown function that flushes and stops both providers.
//
// The OTLP gRPC exporter endpoint is configured via the standard
// OTEL_EXPORTER_OTLP_ENDPOINT environment variable (e.g. "http://localhost:4317").
// No explicit endpoint is set here — the SDK reads it automatically.
func initOTel(ctx context.Context, serviceName string) (shutdown func(context.Context) error, err error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTel resource: %w", err)
	}

	// --- Traces ----------------------------------------------------------
	traceExporter, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(traceExporter),
		trace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// --- Metrics ---------------------------------------------------------
	metricExporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		// Best-effort: shut down the tracer provider before returning.
		tp.Shutdown(ctx) //nolint:errcheck
		return nil, fmt.Errorf("failed to create OTLP metric exporter: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExporter)),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	// --- Shutdown function -----------------------------------------------
	shutdown = func(ctx context.Context) error {
		var errs []error
		if err := tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer provider shutdown: %w", err))
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
