package telemetry

import (
	"context"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// ProtectedSpanProcessor is the protected lane for the spans the engine can't
// afford to lose, chosen at each span's start (IsProtectedSpan):
//
//   - Call spans: spans carrying their own encoded call frame as
//     dagger.io/dag.call (core/telemetry.go). That span copy is the only
//     carrier of a spanned call's frame — the producer claims the digest and
//     skips its payload log — so losing the span loses a frame a client, an
//     archive seal or a Cloud restore may need to rebuild a recipe.
//   - Cache spans: a dagql call's span, its frame on it or not, and a lazy
//     evaluation's span, which end naming a cache entry with its state
//     (dagger.io/cache.*). They are how the remote cache learns the engine's
//     cache, and the session declares how many it ended
//     (dagger.io/cache.session.spans), so a dropped one is a record the
//     remote cache never learns and a count its readers wait on forever.
//
// It exports both the live start snapshot (as otel.LiveSpanProcessor does,
// so clients render a call's arguments while it runs) and the end snapshot,
// through a CoalescingSpanExporter. Both take this lane, so a live snapshot
// can never land after its span's end. Delivery mirrors
// CallPayloadBatchProcessor: an unbounded queue that never blocks or drops on
// overflow, on-demand coalesced export, in-order retry with backoff up to
// CallPayloadMaxExportAttempts, ForceFlush reporting only its own pass's drops
// and Shutdown every terminal loss of the processor's lifetime. Shutdown does
// not shut the exporter down.
//
// Pair it with WithoutProtectedSpans around the ordinary live processor, so
// no protected span is exported twice.
type ProtectedSpanProcessor struct {
	*protectedBatcher[sdktrace.ReadOnlySpan]
}

var _ sdktrace.SpanProcessor = (*ProtectedSpanProcessor)(nil)

func NewProtectedSpanProcessor(exporter sdktrace.SpanExporter) *ProtectedSpanProcessor {
	coalescing := telemetry.CoalescingSpanExporter{SpanExporter: exporter}
	return &ProtectedSpanProcessor{
		protectedBatcher: newProtectedBatcher(protectedBatcherConfig[sdktrace.ReadOnlySpan]{
			export:    coalescing.ExportSpans,
			describe:  callSpanDigests,
			batchSize: LargeSpanExportBatchSize,
			kind:      "spans",
		}),
	}
}

func (processor *ProtectedSpanProcessor) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	if span == nil || !IsProtectedSpan(span) {
		return
	}
	// Freeze the start snapshot now: the live span keeps changing, and the
	// export may run after it ended (see otel.LiveSpanProcessor).
	_ = processor.enqueue(telemetry.SnapshotSpan(span))
}

func (processor *ProtectedSpanProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	if span == nil || !IsProtectedSpan(span) {
		return
	}
	_ = processor.enqueue(span)
}

// IsProtectedSpan reports whether a span takes the protected lane: a call
// span, or a span whose producer reports cache state on it when it ends.
// Both are known by attributes stamped at the span's start, so a span takes
// the same lane at its start and at its end: core.AroundFunc stamps every
// dagql call's span with dagger.io/dag.inputs when it can compute them (it
// logs when it can't), and the cache stamps a lazy evaluation's with
// wcprof.op.kind "lazy".
func IsProtectedSpan(span sdktrace.ReadOnlySpan) bool {
	for _, attr := range span.Attributes() {
		switch string(attr.Key) {
		case telemetry.DagCallAttr:
			if attr.Value.Type() == attribute.STRING && attr.Value.AsString() != "" {
				return true
			}
		case telemetry.DagInputsAttr:
			return true
		case telemetryattrs.WcprofOpKindAttr:
			if attr.Value.AsString() == wcprof.OpKindLazy.String() {
				return true
			}
		}
	}
	return false
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
	return CallAttrsDigest(span.Attributes())
}

// CallAttrsDigest is CallSpanDigest of a span's attributes, for a caller
// that has already read them.
func CallAttrsDigest(attrs []attribute.KeyValue) (digest string, ok bool) {
	hasCall := false
	for _, attr := range attrs {
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

// WithoutProtectedSpans wraps an ordinary span processor so protected spans
// never reach it: ProtectedSpanProcessor carries them, and the ordinary
// bounded queue would both duplicate them and let them be dropped on
// overflow.
func WithoutProtectedSpans(next sdktrace.SpanProcessor) sdktrace.SpanProcessor {
	return withoutProtectedSpansProcessor{next: next}
}

type withoutProtectedSpansProcessor struct {
	next sdktrace.SpanProcessor
}

func (p withoutProtectedSpansProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	if span != nil && IsProtectedSpan(span) {
		return
	}
	p.next.OnStart(ctx, span)
}

func (p withoutProtectedSpansProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	if span != nil && IsProtectedSpan(span) {
		return
	}
	p.next.OnEnd(span)
}

func (p withoutProtectedSpansProcessor) Shutdown(ctx context.Context) error {
	return p.next.Shutdown(ctx)
}

func (p withoutProtectedSpansProcessor) ForceFlush(ctx context.Context) error {
	return p.next.ForceFlush(ctx)
}
