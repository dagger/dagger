package codexapp

import (
	"context"
	"errors"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The item stream taps the same live telemetry the frontend renders. These
// fan-out exporters let it sit beside the frontend's exporters without the
// telemetry pipeline knowing there are two consumers.

type multiSpanExporter []sdktrace.SpanExporter

// TeeSpanExporters returns a span exporter that forwards to every exporter
// given, in order.
func TeeSpanExporters(exporters ...sdktrace.SpanExporter) sdktrace.SpanExporter {
	return multiSpanExporter(exporters)
}

func (m multiSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	var errs []error
	for _, exp := range m {
		errs = append(errs, exp.ExportSpans(ctx, spans))
	}
	return errors.Join(errs...)
}

func (m multiSpanExporter) Shutdown(ctx context.Context) error {
	var errs []error
	for _, exp := range m {
		errs = append(errs, exp.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

type multiLogExporter []sdklog.Exporter

// TeeLogExporters returns a log exporter that forwards to every exporter
// given, in order.
func TeeLogExporters(exporters ...sdklog.Exporter) sdklog.Exporter {
	return multiLogExporter(exporters)
}

func (m multiLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	var errs []error
	for _, exp := range m {
		errs = append(errs, exp.Export(ctx, records))
	}
	return errors.Join(errs...)
}

func (m multiLogExporter) Shutdown(ctx context.Context) error {
	var errs []error
	for _, exp := range m {
		errs = append(errs, exp.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

func (m multiLogExporter) ForceFlush(ctx context.Context) error {
	var errs []error
	for _, exp := range m {
		errs = append(errs, exp.ForceFlush(ctx))
	}
	return errors.Join(errs...)
}
