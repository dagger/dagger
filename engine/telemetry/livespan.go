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
