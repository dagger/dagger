package dagql

// Emit-path tests for the lazy choke point: drive the REAL
// emit — the real beginOTelLazyOp, the real wcprof.parent stamping processor, the
// real resumedCallbackSpan re-point — against an in-memory SDK tracer and assert the
// exported spans carry the causal-parent override + op-kind/passthrough shape the
// offline analyzer consumes: the processor's parent-id discriminator, the
// producer/no-producer cases, and the coverage that every per-client export path
// keeps the stamp.

import (
	"context"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// newLazyRecordingRoot is newRecordingRoot plus the real stamping processor,
// registered FIRST (mirroring engine/server/session.go), so the stamp lands on
// the span object before the recorder snapshots it.
func newLazyRecordingRoot(name string) (*tracetest.SpanRecorder, context.Context, trace.Span) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(NewWcprofLazyParentProcessor()),
		sdktrace.WithSpanProcessor(sr),
	)
	ctx, root := tp.Tracer("wcprof-otel-lazy-test").Start(context.Background(), name)
	return sr, ctx, root
}

func spanBySpanID(t *testing.T, ended []sdktrace.ReadOnlySpan, id trace.SpanID) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range ended {
		if s.SpanContext().SpanID() == id {
			return s
		}
	}
	t.Fatalf("no exported span with id %s", id)
	return nil
}

// TestLazyEmitProducerRepointStampsDirectChildrenOnly is the load-bearing
// emit-path test for the lazy producer-context case and the stamping processor's
// parent-id discriminator. It drives the real beginOTelLazyOp (producer context captured ⇒
// the lazy op IS the resume span), creates a direct work span and a descendant
// under the re-pointed callback ctx, and asserts on the genuinely-exported spans:
// (1) UI parentage unchanged (direct work still parents to the producer), (2)
// causal re-home on the DIRECT child only (the descendant is unstamped) — the
// exact override shape the offline analyzer reads to re-home the work.
func TestLazyEmitProducerRepointStampsDirectChildrenOnly(t *testing.T) {
	const sessionID = "sess-1"
	const resultID = sharedResultID(42)

	sr, rootCtx, root := newLazyRecordingRoot("POST /query")

	// producer call O — returned the pending result and already ended.
	_, producer := Tracer(rootCtx).Start(rootCtx, "Query.directory")
	producer.End()
	producerSC := producer.SpanContext()

	// consumer call C — triggers evaluation; the lazy op nests under it.
	consumerCtx, consumer := Tracer(rootCtx).Start(rootCtx, "Directory.export")

	// Cache carrying the producer's captured lazy span context (what
	// captureSessionLazySpanContext records at produce time).
	c := &Cache{
		sessionLazySpansBySession: map[string]map[sharedResultID]trace.SpanContext{
			sessionID: {resultID: producerSC},
		},
	}
	evalCtx := engine.ContextWithClientMetadata(consumerCtx, &engine.ClientMetadata{
		SessionID: sessionID,
		ClientID:  "client-1",
	})

	callbackCtx, lazySpan, isResume := c.beginOTelLazyOp(evalCtx, resultID, &ResultCall{Field: "directory"})
	if !isResume {
		t.Fatal("producer context captured ⇒ must be the resume re-point case")
	}
	if lazySpan == nil || !lazySpan.SpanContext().IsValid() {
		t.Fatal("lazy op span must be minted and valid (the joiner wait target)")
	}
	lazyID := lazySpan.SpanContext().SpanID()

	// the deferred work: a DIRECT child (re-pointed under the producer), then a
	// DESCENDANT under it (its parent is the direct child, not the producer).
	w1Ctx, w1 := Tracer(callbackCtx).Start(callbackCtx, "Directory.withNewFile")
	_, w2 := Tracer(w1Ctx).Start(w1Ctx, "exec /bin/sh")
	w2.End()
	w1.End()
	lazySpan.End()
	consumer.End()
	root.End()

	ended := sr.Ended()
	w1Span := spanBySpanID(t, ended, w1.SpanContext().SpanID())
	w2Span := spanBySpanID(t, ended, w2.SpanContext().SpanID())
	lazyExported := spanBySpanID(t, ended, lazyID)

	// (1) UI parentage unchanged: the direct work span still parents to the
	// producer O (the resumedCallbackSpan re-point is preserved), NOT the lazy op.
	if w1Span.Parent().SpanID() != producerSC.SpanID() {
		t.Fatalf("UI parentage must stay the producer: w1.parent=%s want=%s",
			w1Span.Parent().SpanID(), producerSC.SpanID())
	}

	// (2) causal re-home — DIRECT child only.
	if got, ok := attrString(w1Span, telemetryattrs.WcprofParentAttr); !ok || got != lazyID.String() {
		t.Fatalf("direct work span must carry wcprof.parent=%s; got %q ok=%v", lazyID, got, ok)
	}
	if _, ok := attrString(w2Span, telemetryattrs.WcprofParentAttr); ok {
		t.Fatal("a descendant work span must NOT be stamped (over-stamping would flatten the subtree)")
	}

	// the lazy op (the resume span): nests under the consumer (genuine synchronous
	// nesting), passthrough, kind=lazy, name unchanged ("resume <field>").
	if lazyExported.Parent().SpanID() != consumer.SpanContext().SpanID() {
		t.Fatalf("lazy op must nest under the consumer; parent=%s want=%s",
			lazyExported.Parent().SpanID(), consumer.SpanContext().SpanID())
	}
	if k, _ := attrString(lazyExported, telemetryattrs.WcprofOpKindAttr); k != wcprof.OpKindLazy.String() {
		t.Fatalf("lazy op kind = %q, want lazy", k)
	}
	if pass, ok := attrBool(lazyExported, telemetry.UIPassthroughAttr); !ok || !pass {
		t.Fatal("lazy op must be ui.passthrough")
	}
	if lazyExported.Name() != "resume directory" {
		t.Fatalf("producer-case lazy op keeps the UI resume name; got %q", lazyExported.Name())
	}
}

// TestLazyEmitNoProducerNestsUnderHiddenLazyOp covers the no-producer-context
// case: with no captured producer span, beginOTelLazyOp mints a new hidden
// passthrough lazy op under the consumer, the work nests under it by ordinary
// parentId (no re-point, no override), and the op is named by the producing field
// so its class matches native.
func TestLazyEmitNoProducerNestsUnderHiddenLazyOp(t *testing.T) {
	sr, rootCtx, root := newLazyRecordingRoot("POST /query")
	consumerCtx, consumer := Tracer(rootCtx).Start(rootCtx, "Directory.export")

	c := &Cache{} // no captured producer span context
	evalCtx := engine.ContextWithClientMetadata(consumerCtx, &engine.ClientMetadata{
		SessionID: "sess", ClientID: "client",
	})

	callbackCtx, lazySpan, isResume := c.beginOTelLazyOp(evalCtx, sharedResultID(7), &ResultCall{Field: "withNewFile"})
	if isResume {
		t.Fatal("no producer context ⇒ must NOT be the resume re-point case")
	}
	if lazySpan == nil || !lazySpan.SpanContext().IsValid() {
		t.Fatal("a hidden lazy op must still be minted (joiner wait target)")
	}

	w1Ctx, w1 := Tracer(callbackCtx).Start(callbackCtx, "exec build")
	_, w2 := Tracer(w1Ctx).Start(w1Ctx, "subexec")
	w2.End()
	w1.End()
	lazySpan.End()
	consumer.End()
	root.End()

	ended := sr.Ended()
	lazyExported := spanBySpanID(t, ended, lazySpan.SpanContext().SpanID())
	w1Span := spanBySpanID(t, ended, w1.SpanContext().SpanID())

	// hidden lazy op: under the consumer, passthrough, kind=lazy, named by the
	// producing field (profCallClass, matching native's lazy-op class).
	if lazyExported.Parent().SpanID() != consumer.SpanContext().SpanID() {
		t.Fatalf("hidden lazy op must nest under the consumer; parent=%s", lazyExported.Parent().SpanID())
	}
	if k, _ := attrString(lazyExported, telemetryattrs.WcprofOpKindAttr); k != wcprof.OpKindLazy.String() {
		t.Fatalf("hidden lazy op kind = %q, want lazy", k)
	}
	if pass, ok := attrBool(lazyExported, telemetry.UIPassthroughAttr); !ok || !pass {
		t.Fatal("hidden lazy op must be ui.passthrough")
	}
	if want := "Query.withNewFile"; lazyExported.Name() != want {
		t.Fatalf("hidden lazy op name should be the producing field %q; got %q", want, lazyExported.Name())
	}

	// no re-point ⇒ no wcprof.parent stamp; the work nests under the lazy op by
	// ordinary parentId.
	if _, ok := attrString(w1Span, telemetryattrs.WcprofParentAttr); ok {
		t.Fatal("no-producer case must not stamp wcprof.parent (work nests under the lazy op by parentId)")
	}
	if w1Span.Parent().SpanID() != lazySpan.SpanContext().SpanID() {
		t.Fatalf("work must nest under the hidden lazy op by parentId; parent=%s", w1Span.Parent().SpanID())
	}
}

// TestWcprofLazyParentProcessorStampsAllExports asserts the stamp reaches EVERY
// per-client export (the stamping processor must be registered on every provider).
// It builds a provider mirroring engine/server/session.go — the stamping processor FIRST,
// then a LiveSpanProcessor for the client's own DB and one for a parent export —
// and asserts a re-pointed work span carries wcprof.parent in BOTH exporters (and
// in every exported copy, proving the live-start snapshot carries it too), while
// a descendant stays unstamped. If a future export path is added without the
// stamp running first, this fails.
func TestWcprofLazyParentProcessorStampsAllExports(t *testing.T) {
	own := tracetest.NewInMemoryExporter()
	parent := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(NewWcprofLazyParentProcessor()),
		sdktrace.WithSpanProcessor(telemetry.NewLiveSpanProcessor(own)),
		sdktrace.WithSpanProcessor(telemetry.NewLiveSpanProcessor(parent)),
	)
	defer tp.Shutdown(context.Background())

	tr := tp.Tracer("wcprof-otel-coverage-test")
	_, producer := tr.Start(context.Background(), "producer")
	producer.End()
	lazyCtx, lazyOp := tr.Start(context.Background(), "resume foo")

	// reproduce beginOTelLazyOp's producer-case callback ctx.
	callbackCtx := trace.ContextWithSpan(lazyCtx, resumedCallbackSpan{
		Span: lazyOp,
		sc:   producer.SpanContext(),
		tp:   lazyOp.TracerProvider(),
	})
	callbackCtx = withLazyParentOverride(callbackCtx, lazyOp.SpanContext().SpanID(), producer.SpanContext().SpanID())

	directCtx, direct := tr.Start(callbackCtx, "work")
	_, descendant := tr.Start(directCtx, "subwork")
	descendant.End()
	direct.End()
	lazyOp.End()

	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	wantParent := lazyOp.SpanContext().SpanID().String()
	for name, exp := range map[string]*tracetest.InMemoryExporter{"own": own, "parent": parent} {
		if !exportedSpanStampedParent(exp, direct.SpanContext().SpanID(), wantParent) {
			t.Fatalf("%s export: the direct work span lost wcprof.parent=%s — a per-client export path missed the stamp", name, wantParent)
		}
		if exportedSpanHasParentAttr(exp, descendant.SpanContext().SpanID()) {
			t.Fatalf("%s export: a descendant must not be stamped (only direct re-pointed children are)", name)
		}
	}
}

// exportedSpanStampedParent reports whether ≥1 exported copy of span id exists and
// every copy carries wcprof.parent=wantParent (so the live-start snapshot, taken
// after the first-registered stamping processor, carries it too).
func exportedSpanStampedParent(exp *tracetest.InMemoryExporter, id trace.SpanID, wantParent string) bool {
	found := false
	for _, st := range exp.GetSpans() {
		if st.SpanContext.SpanID() != id {
			continue
		}
		found = true
		ok := false
		for _, kv := range st.Attributes {
			if string(kv.Key) == telemetryattrs.WcprofParentAttr && kv.Value.AsString() == wantParent {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return found
}

func exportedSpanHasParentAttr(exp *tracetest.InMemoryExporter, id trace.SpanID) bool {
	for _, st := range exp.GetSpans() {
		if st.SpanContext.SpanID() != id {
			continue
		}
		for _, kv := range st.Attributes {
			if string(kv.Key) == telemetryattrs.WcprofParentAttr {
				return true
			}
		}
	}
	return false
}
