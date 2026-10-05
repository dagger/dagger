package telemetry

import (
	"context"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// CallSpanProcessor is the protected lane for call spans: spans carrying their
// own encoded call frame as dagger.io/dag.call (core/telemetry.go). That span
// copy is the only carrier of a spanned call's frame — the producer claims the
// digest and skips its payload log — so losing the span loses a frame a
// client, an archive seal or a Cloud restore may need to rebuild a recipe.
//
// It exports both the live start snapshot (as otel.LiveSpanProcessor does,
// so clients render a call's arguments while it runs) and the end snapshot,
// through a CoalescingSpanExporter. Delivery mirrors CallPayloadBatchProcessor:
// an unbounded queue that never blocks or drops on overflow, on-demand
// coalesced export, in-order retry with backoff up to
// CallPayloadMaxExportAttempts, ForceFlush reporting only its own pass's drops
// and Shutdown every terminal loss of the processor's lifetime. Shutdown does
// not shut the exporter down.
//
// Pair it with WithoutCallSpans around the ordinary live processor, so no
// call span is exported twice.
type CallSpanProcessor struct {
	*protectedBatcher[sdktrace.ReadOnlySpan]
}

var _ sdktrace.SpanProcessor = (*CallSpanProcessor)(nil)

func NewCallSpanProcessor(exporter sdktrace.SpanExporter) *CallSpanProcessor {
	coalescing := telemetry.CoalescingSpanExporter{SpanExporter: exporter}
	return &CallSpanProcessor{
		protectedBatcher: newProtectedBatcher(protectedBatcherConfig[sdktrace.ReadOnlySpan]{
			export:    coalescing.ExportSpans,
			describe:  callSpanDigests,
			batchSize: LargeSpanExportBatchSize,
			kind:      "spans",
		}),
	}
}

func (processor *CallSpanProcessor) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	if span == nil || !IsCallSpan(span) {
		return
	}
	// Freeze the start snapshot now: the live span keeps changing, and the
	// export may run after it ended (see otel.LiveSpanProcessor).
	_ = processor.enqueue(telemetry.SnapshotSpan(span))
}

func (processor *CallSpanProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	if span == nil || !IsCallSpan(span) {
		return
	}
	_ = processor.enqueue(span)
}

// IsCallSpan reports whether a span carries its call's encoded frame
// (dagger.io/dag.call), which makes it the frame's delivery.
func IsCallSpan(span sdktrace.ReadOnlySpan) bool {
	_, ok := CallSpanDigest(span)
	return ok
}

// CallSpanDigest returns the digest a call span delivers the frame of: its
// dagger.io/dag.digest, the key the producer claims the payload under. ok is
// false for spans that carry no frame.
func CallSpanDigest(span sdktrace.ReadOnlySpan) (digest string, ok bool) {
	hasCall := false
	for _, attr := range span.Attributes() {
		switch string(attr.Key) {
		case telemetry.DagCallAttr:
			hasCall = attr.Value.Type() == attribute.STRING && attr.Value.AsString() != ""
		case telemetry.DagDigestAttr:
			if attr.Value.Type() == attribute.STRING {
				digest = attr.Value.AsString()
			}
		}
	}
	return digest, hasCall
}

func callSpanDigests(spans []sdktrace.ReadOnlySpan) []string {
	digests := make([]string, 0, len(spans))
	for _, span := range spans {
		digest, _ := CallSpanDigest(span)
		if digest == "" {
			digest = "?"
		}
		digests = append(digests, digest)
	}
	return digests
}

// WithoutCallSpans wraps an ordinary span processor so call spans never reach
// it: CallSpanProcessor carries them, and the ordinary bounded queue would
// both duplicate them and let them be dropped on overflow.
func WithoutCallSpans(next sdktrace.SpanProcessor) sdktrace.SpanProcessor {
	return withoutCallSpansProcessor{next: next}
}

type withoutCallSpansProcessor struct {
	next sdktrace.SpanProcessor
}

func (p withoutCallSpansProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	if span != nil && IsCallSpan(span) {
		return
	}
	p.next.OnStart(ctx, span)
}

func (p withoutCallSpansProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	if span != nil && IsCallSpan(span) {
		return
	}
	p.next.OnEnd(span)
}

func (p withoutCallSpansProcessor) Shutdown(ctx context.Context) error {
	return p.next.Shutdown(ctx)
}

func (p withoutCallSpansProcessor) ForceFlush(ctx context.Context) error {
	return p.next.ForceFlush(ctx)
}
