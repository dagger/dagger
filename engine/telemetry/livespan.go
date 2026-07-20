package telemetry

import (
	"context"
	"sync/atomic"

	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Enlarged, BOUNDED BatchSpanProcessor sizes for the span hops that carry a trace
// toward Dagger Cloud. The OTel BSP queue is non-blocking and silently DROPS spans
// on overflow; the SDK default of 2048 slots is too small for a burst like a cold
// engine build (~15k spans, live-double-emitted into ~30k records), which is why
// these hops use a larger queue at all.
//
// The queue must stay MODEST as well as bounded, because its worst case is paid
// per PROCESSOR, and processors multiply: every client gets one such processor for
// its own DB plus one per ancestor client, each with an eagerly allocated ring
// (16 bytes/slot) that can fill with full span snapshots whenever the SQLite
// writer falls behind. A heavily grouped CI session can hold dozens of clients, so
// per-processor worst case × processor count is the engine's telemetry memory
// ceiling. At 16Ki slots the per-processor ceiling is ~16 MiB of retained
// snapshots (at ~1 KiB each) plus a 256 KiB ring — 16x below the previous 256Ki
// sizing, which put a loaded session's aggregate worst case far past the physical
// memory of a typical CI runner — while still giving 8x the SDK default's burst
// headroom.
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
// exporter (internal/cmd/dagger) so a big-burst trace arrives complete. The engine's
// per-client DB processors use NewInternalFilteringLiveSpanProcessor instead.
func NewLargeQueueLiveSpanProcessor(exp sdktrace.SpanExporter) *telemetry.LiveSpanProcessor {
	return &telemetry.LiveSpanProcessor{
		SpanProcessor: newLargeQueueBSP(exp),
	}
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

// liveSkipLogInterval is how many suppressed internal live-emits between progress
// log lines (a coarse, process-global counter — one line per this many skips).
const liveSkipLogInterval = 10000

var (
	// liveInternalSkipped counts live (OnStart) snapshots suppressed because the
	// span is dagger.io/ui.internal, across every InternalFiltering processor in
	// this engine process.
	liveInternalSkipped atomic.Int64
	// liveEmitted counts live snapshots actually emitted (non-internal spans).
	// Together with liveInternalSkipped it gives the fraction of the live
	// double-emit we dropped: skipped / (skipped + emitted).
	liveEmitted atomic.Int64
)

// NewInternalFilteringLiveSpanProcessor is NewLargeQueueLiveSpanProcessor that
// additionally SKIPS the live (OnStart) double-emit for dagger.io/ui.internal
// spans. Those spans are hidden from the UI by default, so their live snapshot is
// pure write volume on the per-client SQLite DBs with zero live-rendering value —
// yet it doubles their write cost, and internal spans (the reflection/schema-walk
// class, plus the childless dagql.publishResult leaf) are a large share of a
// module-load trace. The final (OnEnd) span is still written, so nothing is lost
// from the completed trace or Cloud; only the running snapshot is dropped.
//
// Only genuinely childless-or-hidden spans should be marked Internal: a span the
// UI renders (or whose children it promotes, e.g. a Passthrough call_exec twin)
// must keep its live snapshot, else its visible descendants are orphaned live.
func NewInternalFilteringLiveSpanProcessor(exp sdktrace.SpanExporter) sdktrace.SpanProcessor {
	return &internalFilteringLiveSpanProcessor{
		SpanProcessor: newLargeQueueBSP(exp),
	}
}

// internalFilteringLiveSpanProcessor is a LiveSpanProcessor (OnStart re-emits the
// span as a live snapshot) that suppresses that snapshot for internal spans.
type internalFilteringLiveSpanProcessor struct {
	sdktrace.SpanProcessor // underlying large-queue BatchSpanProcessor
}

func (p *internalFilteringLiveSpanProcessor) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	if spanIsInternal(span) {
		n := liveInternalSkipped.Add(1)
		if n%liveSkipLogInterval == 0 {
			emitted := liveEmitted.Load()
			var pct float64
			if total := n + emitted; total > 0 {
				pct = 100 * float64(n) / float64(total)
			}
			slog.Info("suppressed internal-span live double-emit (write-volume reduction)",
				"skippedInternalLiveEmits", n,
				"liveEmitted", emitted,
				"skipPctOfLiveEmits", pct)
		}
		return
	}
	liveEmitted.Add(1)
	// Send a read-only snapshot of the live span downstream (matches
	// otel.LiveSpanProcessor.OnStart) so a running span is visible before it ends.
	p.SpanProcessor.OnEnd(telemetry.SnapshotSpan(span))
}

// spanIsInternal reports whether the span carries dagger.io/ui.internal=true. The
// attribute is set at span creation (telemetry.Internal() is a SpanStartOption),
// so it is already present here in OnStart; spans marked internal only later via
// SetAttributes are not caught and keep their live emit.
func spanIsInternal(span sdktrace.ReadWriteSpan) bool {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == telemetry.UIInternalAttr {
			return kv.Value.AsBool()
		}
	}
	return false
}
