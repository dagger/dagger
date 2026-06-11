package telemetry

import (
	"context"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type SharedSpanExporter struct {
	sdktrace.SpanExporter
}

func (SharedSpanExporter) Shutdown(context.Context) error {
	return nil
}

type SharedLogExporter struct {
	sdklog.Exporter
}

func (SharedLogExporter) Shutdown(context.Context) error {
	return nil
}

type SharedMetricExporter struct {
	sdkmetric.Exporter
}

func (SharedMetricExporter) Shutdown(context.Context) error {
	return nil
}
