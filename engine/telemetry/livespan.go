package telemetry

import (
	"context"
	"sync/atomic"

	telemetry "github.com/dagger/otel-go"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/engine/slog"
)

// Enlarged, BOUNDED BatchSpanProcessor sizes for the span hops that carry a trace
// toward Dagger Cloud. The OTel BSP queue is non-blocking and silently DROPS spans
// on overflow; the SDK default of 2048 slots is too small for a burst like a cold
// engine build (~15k spans, live-double-emitted into ~30k records), which is why
// these hops use a larger queue at all.
//
// The queue must stay MODEST as well as bounded, because its worst case is paid
// per-processor worst case used to multiply by client and ancestor counts. The
// engine server now owns one processor per session and routes snapshots by their
// stamped origin ID; the bounded queue still protects against burst retention.
//
// Kept BOUNDED — never BlockOnQueueFull — so telemetry can NEVER stall the build.
// If a burst still overflows, spans are dropped rather than retained: the wcprof
// completeness checksum (received < declared, see engine/server/wcprofcount.go)
// catches the loss loudly and the offline analyzer refuses the trace instead of
// silently ranking on partial data.
const (
	LargeSpanQueueSize       = 16384 // 16Ki — 8x the SDK default; bounds retained snapshots per hop
	LargeSpanExportBatchSize = 2048  // drains a full queue in 8 batches; bounds per-batch references
)

// NewLargeQueueLiveSpanProcessor is otel.NewLiveSpanProcessor with the enlarged
// bounded queue above in place of the default 2048-slot one. Used on the CLI→Cloud
// exporter (internal/cmd/dagger) and the engine's per-client store exporters so a
// big-burst trace arrives complete. The engine wraps it in WithoutCallSpans:
// call spans, which carry frames nothing else delivers, take the lossless
// CallSpanProcessor instead.
func NewLargeQueueLiveSpanProcessor(exp sdktrace.SpanExporter) *telemetry.LiveSpanProcessor {
	return NewCountedLargeQueueLiveSpanProcessor(exp, nil)
}

// NewCountedLargeQueueLiveSpanProcessor is NewLargeQueueLiveSpanProcessor
// counting, in counts if it isn't nil, the spans its queue takes in and hands
// on to exp, so its drops can be reported.
func NewCountedLargeQueueLiveSpanProcessor(exp sdktrace.SpanExporter, counts *SpanQueueCounts) *telemetry.LiveSpanProcessor {
	if exp != nil {
		exp = telemetry.CoalescingSpanExporter{SpanExporter: exp}
		if counts != nil {
			exp = countingSpanExporter{SpanExporter: exp, counts: counts}
		}
	}
	bsp := newLargeQueueBSP(exp)
	if counts != nil {
		bsp = countingSpanProcessor{SpanProcessor: bsp, counts: counts}
	}
	return &telemetry.LiveSpanProcessor{
		SpanProcessor: bsp,
	}
}

// SpanQueueCounts counts the spans a bounded span queue takes in and those it
// hands on to its exporter. Once the queue has drained, those it never handed
// on were dropped on overflow.
type SpanQueueCounts struct {
	queued   atomic.Int64
	exported atomic.Int64
}

// Queued returns the spans the queue has taken in.
func (counts *SpanQueueCounts) Queued() int64 {
	return counts.queued.Load()
}

// Dropped returns the spans the queue took in but hasn't handed on: those it
// dropped, and until it has drained, those still queued.
func (counts *SpanQueueCounts) Dropped() int64 {
	return counts.queued.Load() - counts.exported.Load()
}

// LogShutdown logs how many spans the queue dropped, once its processor's
// shutdown returned shutdownErr. A shutdown that completed drained the queue,
// so the count is final. One cut short leaves the queue handing spans on in
// the background, so the count is only of those outstanding.
func (counts *SpanQueueCounts) LogShutdown(lg *slog.Logger, shutdownErr error) {
	lg = lg.With("queued", counts.Queued())
	switch dropped := counts.Dropped(); {
	case shutdownErr != nil:
		lg.Warn("session telemetry span count incomplete", "outstanding", dropped, "error", shutdownErr)
	case dropped > 0:
		lg.Warn("session telemetry dropped spans", "dropped", dropped)
	default:
		lg.Info("session telemetry dropped spans", "dropped", dropped)
	}
}

type countingSpanProcessor struct {
	sdktrace.SpanProcessor
	counts *SpanQueueCounts
}

func (p countingSpanProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	// the batch processor only takes in sampled spans
	if span.SpanContext().IsSampled() {
		p.counts.queued.Add(1)
	}
	p.SpanProcessor.OnEnd(span)
}

type countingSpanExporter struct {
	sdktrace.SpanExporter
	counts *SpanQueueCounts
}

func (exp countingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	exp.counts.exported.Add(int64(len(spans)))
	return exp.SpanExporter.ExportSpans(ctx, spans)
}

func newLargeQueueBSP(exp sdktrace.SpanExporter) sdktrace.SpanProcessor {
	return sdktrace.NewBatchSpanProcessor(
		exp,
		sdktrace.WithMaxQueueSize(LargeSpanQueueSize),
		sdktrace.WithMaxExportBatchSize(LargeSpanExportBatchSize),
		// Preserve near-immediate live export (matches otel.NewLiveSpanProcessor).
		sdktrace.WithBatchTimeout(telemetry.NearlyImmediate),
	)
}
