package telemetry

import (
	"context"
	"errors"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Fan-out exporters for the per-client DB telemetry routes (engine/server).
//
// A batch processor eagerly allocates its full bounded queue the moment it is
// constructed: a large-queue BatchSpanProcessor's LargeSpanQueueSize-slot
// ring, an sdklog BatchProcessor's 2048-slot ring of ~0.5 KiB Records
// (~1 MiB each). The engine used to register one processor per DESTINATION —
// a client's own store plus one per ancestor client — so that fixed cost was
// paid per (client, ancestor) PAIR, and nested clients live for the whole
// session. A long agent session accumulated ~1900 log rings (~1.9 GiB) plus
// ~600 MiB of span queues in a real OOM heap profile; per-processor eager
// queue allocation × processor count was the engine's telemetry memory
// ceiling. One processor per destination also means one poll goroutine per
// destination, multiplying the volume-independent allocation churn described
// on NewLogBatchProcessor.
//
// Fanning a SINGLE processor per signal out across all destinations keeps one
// bounded queue (and one poll loop) per client regardless of nesting depth:
// memory scales with clients, not clients × ancestors. Every record still
// reaches every store — the destinations share the queue, not the delivery.
// Each destination is a store writer that must receive every record, so a
// failure of one must not starve the rest: export continues through the whole
// list and the errors surface joined.

// SpanFanOutExporter delivers every ExportSpans call to each destination in
// order. See the package comment above for why the engine fans out at the
// exporter rather than registering per-destination processors.
type SpanFanOutExporter struct {
	dests []sdktrace.SpanExporter
}

var _ sdktrace.SpanExporter = (*SpanFanOutExporter)(nil)

// NewSpanFanOutExporter fans spans out to every destination exporter.
func NewSpanFanOutExporter(dests ...sdktrace.SpanExporter) *SpanFanOutExporter {
	return &SpanFanOutExporter{dests: dests}
}

// ExportSpans exports to each destination in order. A destination's error
// does not prevent export to the others; all errors are returned joined.
func (e *SpanFanOutExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	var errs []error
	for _, dest := range e.dests {
		errs = append(errs, dest.ExportSpans(ctx, spans))
	}
	return errors.Join(errs...)
}

// Shutdown shuts down every destination, joining any errors.
func (e *SpanFanOutExporter) Shutdown(ctx context.Context) error {
	var errs []error
	for _, dest := range e.dests {
		errs = append(errs, dest.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

// LogFanOutExporter is the log analog of SpanFanOutExporter: every Export
// call is delivered to each destination in order.
type LogFanOutExporter struct {
	dests []sdklog.Exporter
}

var _ sdklog.Exporter = (*LogFanOutExporter)(nil)

// NewLogFanOutExporter fans log records out to every destination exporter.
func NewLogFanOutExporter(dests ...sdklog.Exporter) *LogFanOutExporter {
	return &LogFanOutExporter{dests: dests}
}

// Export exports to each destination in order. A destination's error does
// not prevent export to the others; all errors are returned joined.
func (e *LogFanOutExporter) Export(ctx context.Context, records []sdklog.Record) error {
	var errs []error
	for _, dest := range e.dests {
		errs = append(errs, dest.Export(ctx, records))
	}
	return errors.Join(errs...)
}

// Shutdown shuts down every destination, joining any errors.
func (e *LogFanOutExporter) Shutdown(ctx context.Context) error {
	var errs []error
	for _, dest := range e.dests {
		errs = append(errs, dest.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

// ForceFlush flushes every destination, joining any errors.
func (e *LogFanOutExporter) ForceFlush(ctx context.Context) error {
	var errs []error
	for _, dest := range e.dests {
		errs = append(errs, dest.ForceFlush(ctx))
	}
	return errors.Join(errs...)
}
