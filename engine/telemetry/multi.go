package telemetry

import (
	"context"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// MultiSpanExporter fans out span exports to multiple exporters.
type MultiSpanExporter []sdktrace.SpanExporter

var _ sdktrace.SpanExporter = MultiSpanExporter(nil)

func (m MultiSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	var firstErr error
	for _, exp := range m {
		if err := exp.ExportSpans(ctx, spans); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m MultiSpanExporter) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, exp := range m {
		if err := exp.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// MultiLogExporter fans out log exports to multiple exporters.
type MultiLogExporter []sdklog.Exporter

var _ sdklog.Exporter = MultiLogExporter(nil)

func (m MultiLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	var firstErr error
	for _, exp := range m {
		if err := exp.Export(ctx, records); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m MultiLogExporter) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, exp := range m {
		if err := exp.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m MultiLogExporter) ForceFlush(ctx context.Context) error {
	var firstErr error
	for _, exp := range m {
		if err := exp.ForceFlush(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
