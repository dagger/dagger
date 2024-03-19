package telemetry

import (
	"context"
	"log/slog"

	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/moby/buildkit/identity"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"golang.org/x/sync/errgroup"
)

type MultiSpanExporter []sdktrace.SpanExporter

var _ sdktrace.SpanExporter = MultiSpanExporter{}

func (m MultiSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	eg := new(errgroup.Group)
	for _, e := range m {
		e := e
		eg.Go(func() error {
			return e.ExportSpans(ctx, spans)
		})
	}
	return eg.Wait()
}

func (m MultiSpanExporter) Shutdown(ctx context.Context) error {
	eg := new(errgroup.Group)
	for _, e := range m {
		e := e
		eg.Go(func() error {
			return e.Shutdown(ctx)
		})
	}
	return eg.Wait()
}

// FilterLiveSpansExporter is a SpanExporter that filters out spans that are
// currently running, as indicated by an end time older than its start time
// (typically year 1753).
type FilterLiveSpansExporter struct {
	// sdktrace.SpanProcessor
	sdktrace.SpanExporter
}

// ExportSpans passes each span to the span processor's OnEnd hook so that it
// can be batched and emitted more efficiently.
func (exp FilterLiveSpansExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	batch := identity.NewID()
	filtered := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		if span.StartTime().After(span.EndTime()) {
			slog.Debug("skipping unfinished span", "batch", batch, "span", span.Name(), "id", span.SpanContext().SpanID())
		} else {
			slog.Debug("keeping finished span", "batch", batch, "span", span.Name(), "id", span.SpanContext().SpanID())
			filtered = append(filtered, span)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return exp.SpanExporter.ExportSpans(ctx, filtered)
	// for _, span := range spans {
	// 	if span.StartTime().After(span.EndTime()) {
	// 		slog.Debug("skipping unfinished span", "span", span)
	// 	} else {
	// 		slog.Warn("exporting finished span", "span", span.Name())
	// 		exp.SpanProcessor.OnEnd(span)
	// 		exp.SpanProcessor.ForceFlush(ctx)
	// 	}
	// }
	// exp.SpanProcessor.ForceFlush(ctx)
	// return nil
}

type MultiLogExporter []sdklog.LogExporter

var _ sdklog.LogExporter = MultiLogExporter{}

func (m MultiLogExporter) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	eg := new(errgroup.Group)
	for _, e := range m {
		e := e
		eg.Go(func() error {
			return e.ExportLogs(ctx, logs)
		})
	}
	return eg.Wait()
}

func (m MultiLogExporter) Shutdown(ctx context.Context) error {
	eg := new(errgroup.Group)
	for _, e := range m {
		e := e
		eg.Go(func() error {
			return e.Shutdown(ctx)
		})
	}
	return eg.Wait()
}
