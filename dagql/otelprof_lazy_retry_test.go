package dagql

// Regression tests for two lazy-emit hazards:
//   - the stale lazyEvalSpanCtx bug: a failed traced lazy eval must not leave a
//     wait target that a later untraced retry leader fails to overwrite, which a
//     traced joiner would then mis-link to the wrong (old) lazy op instead of
//     emitting a gate-observable targetless wait.
//   - nested lazy override composition: a re-pointed work span that itself
//     triggers a second beginOTelLazyOp must keep the inner/outer overrides
//     separate (no cross-stamping), validated by a committed unit test rather
//     than only the empirical engine run.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// TestLazyEmitRetryDoesNotLeakStaleWaitTarget is the regression for the stale
// lazyEvalSpanCtx bug. It drives the real evaluateOne path with three actors on
// one shared pending result: a TRACED leader whose eval fails (minting a lazy op),
// an UNTRACED retry leader (which must NOT re-mint), and a TRACED joiner of the
// retry. Without the per-attempt reset, the joiner would read the failed
// attempt's stale span context and emit a resolvable wait to the wrong lazy op;
// with the fix it reads an invalid target and emits a gate-observable targetless
// wait. Asserts the joiner's emitted lazy wait link has an invalid target.
func TestLazyEmitRetryDoesNotLeakStaleWaitTarget(t *testing.T) {
	t.Parallel()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(NewWcprofLazyParentProcessor()),
		sdktrace.WithSpanProcessor(sr),
	)
	tr := tp.Tracer("wcprof-otel-retry-test")

	baseCtx := cacheTestContext(t.Context())
	c, err := NewCache(baseCtx, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	baseCtx = ContextWithCache(baseCtx, c)
	srv := cacheTestServer(t)
	sessionID := cacheTestSessionID(t, baseCtx)

	var calls atomic.Int32
	leaderReady := make(chan struct{})
	release := make(chan struct{})
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "retry-lazy",
	}
	resAny, err := c.GetOrInitCall(baseCtx, sessionID, srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
			Value: 1,
			lazyEval: func(context.Context) error {
				if calls.Add(1) == 1 {
					return errors.New("first lazy attempt fails (retryable)")
				}
				// The retry leader's callback: lazyEvalWaitCh is published now,
				// so signal and block long enough for the joiner to join.
				close(leaderReady)
				<-release
				return nil
			},
		}), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result := resAny.(ObjectResult[*cacheTestObject])
	shared := result.cacheSharedResult()

	// L1: a TRACED leader whose eval fails. It mints a lazy op (valid span ctx);
	// the failure is retryable so the result stays pending.
	l1Ctx, l1Span := tr.Start(baseCtx, "L1-traced-leader")
	if err := c.Evaluate(l1Ctx, result); err == nil {
		t.Fatal("first (traced) lazy attempt must fail and stay retryable")
	}
	l1Span.End()

	// L2: an UNTRACED leader retries (OTelProfActive(evalCtx) is false on the
	// untraced base ctx), so beginOTelLazyOp is not called and must not leave a
	// stale target behind.
	var l2err error
	l2done := make(chan struct{})
	go func() { defer close(l2done); l2err = c.Evaluate(baseCtx, result) }()
	select {
	case <-leaderReady:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the untraced retry leader to publish lazyEvalWaitCh")
	}

	// J: a TRACED joiner of the retry. It reads the wait target under lazyMu and
	// emits its lazy wait after the eval completes.
	jCtx, jSpan := tr.Start(baseCtx, "J-traced-joiner")
	var jerr error
	jdone := make(chan struct{})
	go func() { defer close(jdone); jerr = c.Evaluate(jCtx, result) }()

	// Wait until the joiner has actually joined (incremented waiters under lazyMu,
	// having already read the target) before releasing the leader — otherwise the
	// leader could complete first and the joiner would skip the join entirely.
	deadline := time.Now().Add(5 * time.Second)
	for {
		shared.lazyMu.Lock()
		joined := shared.lazyEvalWaiters >= 2
		shared.lazyMu.Unlock()
		if joined {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the traced joiner to join the retry")
		}
		time.Sleep(time.Millisecond)
	}

	close(release)
	<-l2done
	<-jdone
	jSpan.End()
	if l2err != nil {
		t.Fatalf("untraced retry leader eval: %v", l2err)
	}
	if jerr != nil {
		t.Fatalf("traced joiner eval: %v", jerr)
	}

	// The joiner must have emitted a lazy wait link, and its target must be INVALID
	// (gate-observable targetless), NOT a resolvable link to the first attempt's
	// lazy op.
	jExported := spanBySpanID(t, sr.Ended(), jSpan.SpanContext().SpanID())
	var found bool
	for _, l := range jExported.Links() {
		attrs := map[string]string{}
		for _, kv := range l.Attributes {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
		if attrs[telemetry.LinkPurposeAttr] != telemetryattrs.LinkPurposeWait {
			continue
		}
		if attrs[telemetryattrs.WcprofWaitReasonAttr] != wcprof.WaitReasonLazy.String() {
			continue
		}
		found = true
		if l.SpanContext.IsValid() {
			t.Fatalf("a traced joiner of an untraced retry must emit a TARGETLESS (gate-observable) lazy wait, not a stale link to the failed attempt's lazy op; got resolvable target %s", l.SpanContext.SpanID())
		}
	}
	if !found {
		t.Fatal("the traced joiner must emit a lazy wait link (the load-bearing edge)")
	}
}

// TestLazyEmitNestedOverrideNoCrossStamping is the unit test for nested
// lazy override composition: a deferred-work span
// that itself triggers a second beginOTelLazyOp. The inner eval replaces the lazy
// override for its own subtree, with a distinct producer span id, so the inner
// work re-homes to the INNER lazy op and the outer work to the OUTER lazy op —
// no cross-stamping. Drives the real beginOTelLazyOp + stamping processor.
func TestLazyEmitNestedOverrideNoCrossStamping(t *testing.T) {
	const sessionID = "sess-nested"
	const outerResultID = sharedResultID(100)
	const innerResultID = sharedResultID(200)

	sr, rootCtx, root := newLazyRecordingRoot("POST /query")

	// Two distinct producers (their span ids are the discriminator keys).
	_, outerProducer := Tracer(rootCtx).Start(rootCtx, "Query.outerDir")
	outerProducer.End()
	_, innerProducer := Tracer(rootCtx).Start(rootCtx, "Query.innerDir")
	innerProducer.End()

	consumerCtx, consumer := Tracer(rootCtx).Start(rootCtx, "Directory.export")

	c := &Cache{
		sessionLazySpansBySession: map[string]map[sharedResultID]trace.SpanContext{
			sessionID: {
				outerResultID: outerProducer.SpanContext(),
				innerResultID: innerProducer.SpanContext(),
			},
		},
	}

	// Outer lazy eval, triggered by the consumer.
	outerEvalCtx := engine.ContextWithClientMetadata(consumerCtx,
		&engine.ClientMetadata{SessionID: sessionID, ClientID: "c"})
	outerCBCtx, outerLazy, outerIsResume := c.beginOTelLazyOp(outerEvalCtx, outerResultID, &ResultCall{Field: "outerDir"})
	if !outerIsResume {
		t.Fatal("outer: producer context captured ⇒ resume re-point case")
	}

	// Outer deferred work (direct re-pointed child ⇒ stamped to the outer lazy op).
	outerWorkCtx, outerWork := Tracer(outerCBCtx).Start(outerCBCtx, "Directory.outerWork")

	// The outer work itself triggers a NESTED lazy eval (its own beginOTelLazyOp on
	// the outer-work subtree ctx, which still carries the outer override).
	innerEvalCtx := engine.ContextWithClientMetadata(outerWorkCtx, &engine.ClientMetadata{SessionID: sessionID, ClientID: "c"})
	innerCBCtx, innerLazy, innerIsResume := c.beginOTelLazyOp(innerEvalCtx, innerResultID, &ResultCall{Field: "innerDir"})
	if !innerIsResume {
		t.Fatal("inner: producer context captured ⇒ resume re-point case")
	}

	// Inner deferred work (direct re-pointed child ⇒ stamped to the INNER lazy op).
	_, innerWork := Tracer(innerCBCtx).Start(innerCBCtx, "Directory.innerWork")

	innerWork.End()
	innerLazy.End()
	outerWork.End()
	outerLazy.End()
	consumer.End()
	root.End()

	ended := sr.Ended()
	outerLazyID := outerLazy.SpanContext().SpanID()
	innerLazyID := innerLazy.SpanContext().SpanID()
	if outerLazyID == innerLazyID {
		t.Fatal("inner and outer lazy ops must be distinct spans")
	}

	outerWorkSpan := spanBySpanID(t, ended, outerWork.SpanContext().SpanID())
	innerWorkSpan := spanBySpanID(t, ended, innerWork.SpanContext().SpanID())

	if got, ok := attrString(outerWorkSpan, telemetryattrs.WcprofParentAttr); !ok || got != outerLazyID.String() {
		t.Fatalf("outer work must re-home to the OUTER lazy op; wcprof.parent=%q want=%s", got, outerLazyID)
	}
	if got, ok := attrString(innerWorkSpan, telemetryattrs.WcprofParentAttr); !ok || got != innerLazyID.String() {
		t.Fatalf("inner work must re-home to the INNER lazy op; wcprof.parent=%q want=%s", got, innerLazyID)
	}
	// The crux: no cross-stamping in either direction.
	if got, _ := attrString(outerWorkSpan, telemetryattrs.WcprofParentAttr); got == innerLazyID.String() {
		t.Fatal("cross-stamp: outer work must NOT re-home to the inner lazy op")
	}
	if got, _ := attrString(innerWorkSpan, telemetryattrs.WcprofParentAttr); got == outerLazyID.String() {
		t.Fatal("cross-stamp: inner work must NOT re-home to the outer lazy op")
	}
}
