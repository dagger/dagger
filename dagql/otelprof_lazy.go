package dagql

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
	telemetry "github.com/dagger/otel-go"
)

// OTel emission for lazy / deferred evaluation — the
// subtlest faithfulness break. A resolver may return a *pending* result and
// defer materialization; later some consumer forces it (Cache.Evaluate ->
// evaluateOne). For UI reasons the engine re-points the deferred work's spans
// under the *producer* call that created the pending value (resumedCallbackSpan),
// even though that producer span has already ended — so feeding parentId to the
// analyzer would invent an impossible "producer waited for work it never could"
// edge (a cycle, or work anchored to a parent that never reached its spawn).
//
// The fix keeps the UI re-point exactly as today (no span moves; dagui renders
// unchanged) and separates *causal* parentage from *UI* parentage: the engine
// stamps an explicit wcprof.parent causal-parent override on the DIRECT
// re-pointed work spans, pointing at a consumer-side `lazy` op. The loader reads
// wcprof.parent ?? parentId, so UI reads parentId and the
// analyzer reads the override. This is emit, never inference: the loader only
// reads the attribute the engine writes here.
//
// Like the other OTel-profiling hooks (otelprof_hooks.go) this is gated only on
// telemetry being active, not on wcprof.Enabled — the OTel source must be
// reconstructable from a Cloud trace alone, with no native recorder running.

// lazyParentOverride is carried on the lazy-evaluation callback context so the
// stamping processor can re-home the deferred work to the lazy op causally,
// without moving any span.
type lazyParentOverride struct {
	// lazyOpSpanID is the causal parent the loader should use: the consumer-side
	// lazy op. Stamped as wcprof.parent (lower-hex span id, matching the loader's
	// id encoding).
	lazyOpSpanID trace.SpanID
	// producerSpanID is resumedCallbackSpan's producer SpanContext — the span the
	// re-point points work at. Only spans whose recorded parent is exactly this
	// (the callback's *direct* children) are stamped; their descendants have their
	// real parent and fall through unstamped, so the work subtree's internal
	// structure survives. This parent-id test is the discriminator — a
	// naive "stamp everything under the lazy context" would over-stamp descendants
	// (the override value is inherited) and flatten the subtree under the lazy op.
	producerSpanID trace.SpanID
}

type lazyParentOverrideKey struct{}

// withLazyParentOverride attaches the causal-parent override to the lazy callback
// context. Only set in the producer-context case (beginOTelLazyOp), where the
// work is re-pointed under the producer; in the no-producer case the work nests
// under the lazy op by ordinary parentId and no override is needed.
func withLazyParentOverride(ctx context.Context, lazyOpSpanID, producerSpanID trace.SpanID) context.Context {
	return context.WithValue(ctx, lazyParentOverrideKey{}, lazyParentOverride{
		lazyOpSpanID:   lazyOpSpanID,
		producerSpanID: producerSpanID,
	})
}

// wcprofLazyParentProcessor stamps the wcprof.parent causal-parent override on
// the *direct* re-pointed lazy-work spans. OnStart receives the
// span's parent context (the SDK passes the original Start context), which
// carries the override iff the span was created under a lazy callback context.
type wcprofLazyParentProcessor struct{}

// NewWcprofLazyParentProcessor returns the span processor that stamps
// wcprof.parent on lazy re-pointed work spans. It must be
// registered on every per-client tracer provider BEFORE the LiveSpanProcessor(s)
// (engine/server/session.go): listed first, its OnStart sets the attribute on the
// shared span object before any live-start snapshot is taken, so the override
// reaches every per-client export — the client's own DB and every parent export
// processor on the same provider, so no export path can drop the stamp. The
// ended span always carries the attribute regardless of ordering (the loader uses
// the ended copy), so correctness does not depend on order — ordering only buys
// live consumers the attribute too.
func NewWcprofLazyParentProcessor() sdktrace.SpanProcessor {
	return wcprofLazyParentProcessor{}
}

func (wcprofLazyParentProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	ov, ok := parent.Value(lazyParentOverrideKey{}).(lazyParentOverride)
	if !ok {
		return
	}
	// Stamp only the callback's DIRECT children — those re-pointed under the
	// producer. Descendants (whose recorded parent is some intermediate work
	// span, not the producer) keep nesting via parentId; stamping them would
	// flatten the work subtree under the lazy op.
	if s.Parent().SpanID() != ov.producerSpanID {
		return
	}
	s.SetAttributes(attribute.String(telemetryattrs.WcprofParentAttr, ov.lazyOpSpanID.String()))
}

func (wcprofLazyParentProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (wcprofLazyParentProcessor) Shutdown(context.Context) error   { return nil }
func (wcprofLazyParentProcessor) ForceFlush(context.Context) error { return nil }

// beginOTelLazyOp mints the OTel `lazy` op span for a deferred evaluation. It
// MUST be called under lazyMu, before lazyEvalWaitCh is published, so a joiner
// that observes the in-flight eval always has a valid wait target (the target is
// minted before the waiter-observable primitive). It returns the context the callback runs under,
// the lazy op span (whose SpanContext the caller stashes as the joiner wait
// target and which the caller ends when the callback finishes), and isResume —
// whether this is a producer-context re-point (so the caller applies the
// resume-span blocked attribution on end, unchanged UI behavior).
//
// Two existence cases, made explicit (this is where the trace stops being
// byte-for-byte identical, and why it does not matter visually):
//
//   - Producer context captured (common): the `lazy` op IS the "resume <field>"
//     span today's code creates inside the eval goroutine — minted here instead,
//     earlier (under the lock), so its SpanContext is a valid wait target. It keeps
//     the same parent/name/links/passthrough; only its start timestamp shifts
//     marginally earlier. resumedCallbackSpan keeps the deferred work's parentId =
//     producer so dagui renders unchanged, and the callback ctx
//     carries the wcprof.parent override so the stamping processor re-homes the
//     direct work spans causally to the lazy op.
//   - No producer context (suppressed/untraced producer): today no resume span is
//     created and the work nests under the consumer. We mint a new hidden
//     ui.passthrough `lazy` op here so joiners have a target and the work nests
//     under it by ordinary parentId — dagui elides the passthrough node, so the
//     work still renders under the consumer (visible tree unchanged, one extra
//     hidden span). No re-point and no override are used.
//
// The op carries wcprof.op.kind=lazy so the loader classifies it without
// guessing. In the no-producer case it is named by the producing field (matching
// native's lazy-op class) since there is no UI-facing span to preserve; in the
// producer case the name stays "resume <field>" (UI-load-bearing), so the
// loader's class for that op is "resume <field>" rather than native's
// profCallClass — a benign divergence because the lazy op's self-time is ~0 and
// it never ranks in the bottleneck oracle.
func (c *Cache) beginOTelLazyOp(evalCtx context.Context, sharedID sharedResultID, resultCall *ResultCall) (context.Context, trace.Span, bool) {
	if clientMD, err := engine.ClientMetadataFromContext(evalCtx); err == nil && clientMD.SessionID != "" {
		if originalSpanCtx, ok := c.sessionLazySpanContext(clientMD.SessionID, sharedID); ok {
			spanName := "resume lazy evaluation"
			if resultCall != nil && resultCall.Field != "" {
				spanName = "resume " + resultCall.Field
			}
			// Lazy failure attribution: link the resume span back to all API spans
			// that installed/own this result in the session (dagui interprets
			// cause-purpose links as "this resume is the cause of those installs
			// failing"). Unchanged from the original goroutine emit.
			installCtxs := c.sessionResultInstallSpanContexts(clientMD.SessionID, sharedID)
			links := lazyResumeLinks(originalSpanCtx, installCtxs)
			resumeCtx, resumeSpan := Tracer(evalCtx).Start(
				evalCtx,
				spanName,
				trace.WithLinks(links...),
				telemetry.Passthrough(),
				trace.WithAttributes(attribute.String(telemetryattrs.WcprofOpKindAttr, wcprof.OpKindLazy.String())),
			)
			callbackCtx := trace.ContextWithSpan(resumeCtx, resumedCallbackSpan{
				Span: resumeSpan,
				sc:   originalSpanCtx,
				tp:   resumeSpan.TracerProvider(),
			})
			callbackCtx = withLazyParentOverride(callbackCtx, resumeSpan.SpanContext().SpanID(), originalSpanCtx.SpanID())
			return callbackCtx, resumeSpan, true
		}
	}
	callbackCtx, lazySpan := Tracer(evalCtx).Start(
		evalCtx,
		profCallClass(resultCall),
		telemetry.Passthrough(),
		trace.WithAttributes(attribute.String(telemetryattrs.WcprofOpKindAttr, wcprof.OpKindLazy.String())),
	)
	return callbackCtx, lazySpan, false
}
