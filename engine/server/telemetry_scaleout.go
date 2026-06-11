package server

import (
	"context"
	"sync"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Scale-out sessions stream the full telemetry of every remote engine back
// through the parent chain to the client. For large check fan-outs that
// firehose can overwhelm the client machine, so by default we drop data the
// UI would never show at normal verbosity before it enters the parent's
// telemetry DB:
//
//   - internal spans (dagger.io/ui.internal), unless they errored
//   - logs belonging to those dropped spans
//   - verbose logs (dagger.io/logs.verbose), e.g. function call results
//
// The full stream is always available in Dagger Cloud, which receives
// telemetry from the remote engine directly. Clients opt out of filtering
// with DAGGER_SCALEOUT_TELEMETRY=full (plumbed via ClientMetadata).
const ScaleOutTelemetryFull = "full"

// maxTrackedDroppedSpans bounds the memory used to correlate logs with
// dropped spans. If a session somehow exceeds it, tracking resets: logs for
// previously-dropped spans then pass through unfiltered, which is harmless.
const maxTrackedDroppedSpans = 1 << 20

type scaleOutTelemetryFilter struct {
	mu      sync.Mutex
	dropped map[trace.SpanID]struct{}
}

func newScaleOutTelemetryFilter() *scaleOutTelemetryFilter {
	return &scaleOutTelemetryFilter{
		dropped: make(map[trace.SpanID]struct{}),
	}
}

func (f *scaleOutTelemetryFilter) Spans(next sdktrace.SpanExporter) sdktrace.SpanExporter {
	return &scaleOutSpanFilter{filter: f, next: next}
}

func (f *scaleOutTelemetryFilter) Logs(next sdklog.Exporter) sdklog.Exporter {
	return &scaleOutLogFilter{filter: f, next: next}
}

type scaleOutSpanFilter struct {
	filter *scaleOutTelemetryFilter
	next   sdktrace.SpanExporter
}

var _ sdktrace.SpanExporter = (*scaleOutSpanFilter)(nil)

func (f *scaleOutSpanFilter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	kept := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	f.filter.mu.Lock()
	for _, span := range spans {
		spanID := span.SpanContext().SpanID()
		if dropSpan(span) {
			if len(f.filter.dropped) >= maxTrackedDroppedSpans {
				f.filter.dropped = make(map[trace.SpanID]struct{})
			}
			f.filter.dropped[spanID] = struct{}{}
			continue
		}
		// spans are exported live, in snapshots: an earlier snapshot may have
		// been dropped (e.g. before the span errored), so un-drop it
		delete(f.filter.dropped, spanID)
		kept = append(kept, span)
	}
	f.filter.mu.Unlock()
	if len(kept) == 0 {
		return nil
	}
	return f.next.ExportSpans(ctx, kept)
}

func (f *scaleOutSpanFilter) ForceFlush(ctx context.Context) error {
	if ff, ok := f.next.(interface{ ForceFlush(context.Context) error }); ok {
		return ff.ForceFlush(ctx)
	}
	return nil
}

func (f *scaleOutSpanFilter) Shutdown(ctx context.Context) error {
	return f.next.Shutdown(ctx)
}

// dropSpan reports whether a span is hidden at normal verbosity and safe to
// drop at the scale-out boundary. Errored spans are always kept since the UI
// reveals failures regardless of verbosity.
func dropSpan(span sdktrace.ReadOnlySpan) bool {
	if span.Status().Code == codes.Error {
		return false
	}
	for _, attr := range span.Attributes() {
		if string(attr.Key) == telemetry.UIInternalAttr && attr.Value.AsBool() {
			return true
		}
	}
	return false
}

type scaleOutLogFilter struct {
	filter *scaleOutTelemetryFilter
	next   sdklog.Exporter
}

var _ sdklog.Exporter = (*scaleOutLogFilter)(nil)

func (f *scaleOutLogFilter) Export(ctx context.Context, records []sdklog.Record) error {
	kept := make([]sdklog.Record, 0, len(records))
	for _, rec := range records {
		if f.dropLog(rec) {
			continue
		}
		kept = append(kept, rec)
	}
	if len(kept) == 0 {
		return nil
	}
	return f.next.Export(ctx, kept)
}

func (f *scaleOutLogFilter) dropLog(rec sdklog.Record) bool {
	verbose := false
	rec.WalkAttributes(func(kv otellog.KeyValue) bool {
		if kv.Key == telemetry.LogsVerboseAttr && kv.Value.AsBool() {
			verbose = true
			return false
		}
		return true
	})
	if verbose {
		return true
	}
	f.filter.mu.Lock()
	_, dropped := f.filter.dropped[rec.SpanID()]
	f.filter.mu.Unlock()
	return dropped
}

func (f *scaleOutLogFilter) ForceFlush(ctx context.Context) error {
	return f.next.ForceFlush(ctx)
}

func (f *scaleOutLogFilter) Shutdown(ctx context.Context) error {
	return f.next.Shutdown(ctx)
}
