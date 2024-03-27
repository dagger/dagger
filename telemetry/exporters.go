package telemetry

import (
	"context"
	"log/slog"

	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/moby/buildkit/identity"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
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

type SpanForwarder struct {
	Processors []sdktrace.SpanProcessor
}

var _ sdktrace.SpanExporter = SpanForwarder{}

type discardWritesSpan struct {
	noop.Span
	sdktrace.ReadOnlySpan
}

func (s discardWritesSpan) SpanContext() trace.SpanContext {
	return s.ReadOnlySpan.SpanContext()
}

func (m SpanForwarder) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	eg := new(errgroup.Group)
	for _, p := range m.Processors {
		p := p
		eg.Go(func() error {
			for _, span := range spans {
				if span.EndTime().Before(span.StartTime()) {
					p.OnStart(ctx, discardWritesSpan{noop.Span{}, span})
				} else {
					p.OnEnd(span)
				}
			}
			return nil
		})
	}
	return eg.Wait()
}

func (m SpanForwarder) Shutdown(ctx context.Context) error {
	eg := new(errgroup.Group)
	for _, p := range m.Processors {
		p := p
		eg.Go(func() error {
			return p.Shutdown(ctx)
		})
	}
	return eg.Wait()
}

// FilterLiveSpansExporter is a SpanExporter that filters out spans that are
// currently running, as indicated by an end time older than its start time
// (typically year 1753).
type FilterLiveSpansExporter struct {
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
}

type LogForwarder struct {
	Processors []sdklog.LogProcessor
}

var _ sdklog.LogExporter = LogForwarder{}

func (m LogForwarder) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	eg := new(errgroup.Group)
	for _, e := range m.Processors {
		e := e
		eg.Go(func() error {
			for _, log := range logs {
				e.OnEmit(ctx, log)
			}
			return nil
		})
	}
	return eg.Wait()
}

func (m LogForwarder) Shutdown(ctx context.Context) error {
	eg := new(errgroup.Group)
	for _, e := range m.Processors {
		e := e
		eg.Go(func() error {
			return e.Shutdown(ctx)
		})
	}
	return eg.Wait()
}
