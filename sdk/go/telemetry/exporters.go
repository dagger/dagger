package telemetry

import (
	"context"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"golang.org/x/sync/errgroup"
)

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
	filtered := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		if span.StartTime().After(span.EndTime()) {
		} else {
			filtered = append(filtered, span)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return exp.SpanExporter.ExportSpans(ctx, filtered)
}

type LogForwarder struct {
	Processors []sdklog.Processor
}

var _ sdklog.Exporter = LogForwarder{}

func (m LogForwarder) Export(ctx context.Context, logs []sdklog.Record) error {
	eg := new(errgroup.Group)
	for _, e := range m.Processors {
		e := e
		eg.Go(func() error {
			for _, log := range logs {
				_ = e.OnEmit(ctx, log)
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

func (m LogForwarder) ForceFlush(ctx context.Context) error {
	eg := new(errgroup.Group)
	for _, e := range m.Processors {
		e := e
		eg.Go(func() error {
			return e.ForceFlush(ctx)
		})
	}
	return eg.Wait()
}

type MetricsForwarder struct {
	Readers []sdkmetric.Reader
}

var _ sdkmetric.Exporter = MetricsForwarder{}

func (exp MetricsForwarder) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	eg := new(errgroup.Group)
	for _, r := range exp.Readers {
		r := r
		eg.Go(func() error {
			return r.Collect(ctx, metrics)
		})
	}
	return eg.Wait()
}

func (exp MetricsForwarder) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	// TODO:
	// TODO:
	// TODO:
	panic("idk")
}

func (exp MetricsForwarder) Aggregation(sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	// TODO:
	// TODO:
	// TODO:
	panic("idk")
}

func (exp MetricsForwarder) ForceFlush(ctx context.Context) error {
	// TODO: ???
	// TODO: ???
	// TODO: ???
	return nil
}

func (exp MetricsForwarder) Shutdown(ctx context.Context) error {
	eg := new(errgroup.Group)
	for _, r := range exp.Readers {
		eg.Go(func() error {
			return r.Shutdown(ctx)
		})
	}
	return eg.Wait()
}
