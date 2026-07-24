package telemetry

import (
	"context"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Shared*Exporter wrap an exporter whose lifecycle is owned elsewhere (e.g.
// by the session rather than by each client's provider): Shutdown becomes a
// no-op so one consumer shutting down doesn't break the exporter for everyone
// else. The owner shuts the underlying exporter down directly.

type SharedSpanExporter struct {
	sdktrace.SpanExporter
}

func (SharedSpanExporter) Shutdown(context.Context) error { return nil }

type SharedLogExporter struct {
	sdklog.Exporter
}

func (SharedLogExporter) Shutdown(context.Context) error { return nil }

type SharedMetricExporter struct {
	sdkmetric.Exporter
}

func (SharedMetricExporter) Shutdown(context.Context) error { return nil }
