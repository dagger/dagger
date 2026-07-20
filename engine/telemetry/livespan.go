package telemetry

import (
	telemetry "github.com/dagger/otel-go"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Large, BOUNDED BatchSpanProcessor sizes for the span hops that carry a trace
// toward Dagger Cloud. The OTel BSP queue is non-blocking and silently DROPS spans
// on overflow, and the default 2048 slots are far too small for a burst like a cold
// engine build: that is ~15k spans, and live export double-emits each span (a start
// snapshot plus the end) for ~30k records arriving in a tight burst. Sizing the
// queue to 256Ki slots with a large export batch absorbs that burst with generous
// headroom so nothing is dropped (e.g. the wcprof.session_complete completeness
// carrier, which rides at the very tail and was the first thing to drop).
//
// Kept BOUNDED — never BlockOnQueueFull — so telemetry can NEVER stall the build; if
// something still overflows, the wcprof completeness checksum catches it (received <
// declared, hard-fail) rather than silently ranking on partial data. The queue holds
// span POINTERS, so 256Ki slots is only a ~2MiB ring buffer, not 256Ki span copies.
const (
	LargeSpanQueueSize       = 262144 // 256Ki — ~8x a cold engine build's ~30k live records
	LargeSpanExportBatchSize = 16384  // drain the burst in a few large batches, not hundreds
)

// NewLargeQueueLiveSpanProcessor is otel.NewLiveSpanProcessor with the large bounded
// queue above in place of the default 2048-slot one. Use it on every span hop that
// feeds a Cloud trace — the engine's per-client DB processors (engine/server) and the
// CLI→Cloud exporter (internal/cmd/dagger) — so a big-burst trace arrives complete.
func NewLargeQueueLiveSpanProcessor(exp sdktrace.SpanExporter) *telemetry.LiveSpanProcessor {
	return &telemetry.LiveSpanProcessor{
		SpanProcessor: sdktrace.NewBatchSpanProcessor(
			exp,
			sdktrace.WithMaxQueueSize(LargeSpanQueueSize),
			sdktrace.WithMaxExportBatchSize(LargeSpanExportBatchSize),
			// Preserve near-immediate live export (matches otel.NewLiveSpanProcessor).
			sdktrace.WithBatchTimeout(telemetry.NearlyImmediate),
		),
	}
}
