package telemetry

import sdktrace "go.opentelemetry.io/otel/sdk/trace"

type SharedSpanExporter struct {
	sdktrace.SpanExporter
}
