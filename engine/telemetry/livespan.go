package telemetry

import (
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
// (16 bytes/slot) that can fill with full span snapshots whenever the store
// exporter falls behind. A heavily grouped CI session can hold dozens of clients, so
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
// exporter (internal/cmd/dagger) and the engine's per-client store exporters so a
// big-burst trace arrives complete.
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
