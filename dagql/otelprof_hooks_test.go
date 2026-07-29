package dagql

// Emit-path regression tests: drive the three real emit hooks (beginOTelCallExec /
// beginOTelPublishResult / EmitOTelWait) against an in-memory SDK tracer and assert
// the exported spans/links carry exactly the wcprof OTel attribute/link shape the
// offline analyzer consumes — so the emit contract is machine-checked, not just
// code-reviewed. (The offline loader/gate that consume this shape live in the
// closed-source analyzer; their end-to-end coverage is exercised there via fixtures.)

import (
	"context"
	"strconv"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// newRecordingRoot returns an always-sampling in-memory tracer recorder plus a
// root span whose context drives Tracer(ctx) for the hooks under test.
func newRecordingRoot(name string) (*tracetest.SpanRecorder, context.Context, trace.Span) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sr),
	)
	ctx, root := tp.Tracer("wcprof-otel-test").Start(context.Background(), name)
	return sr, ctx, root
}

func spanByKind(t *testing.T, ended []sdktrace.ReadOnlySpan, kind string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range ended {
		for _, kv := range s.Attributes() {
			if string(kv.Key) == telemetryattrs.WcprofOpKindAttr && kv.Value.AsString() == kind {
				return s
			}
		}
	}
	t.Fatalf("no exported span with %s=%q", telemetryattrs.WcprofOpKindAttr, kind)
	return nil
}

func spanByName(t *testing.T, ended []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range ended {
		if s.Name() == name {
			return s
		}
	}
	t.Fatalf("no exported span named %q", name)
	return nil
}

func attrString(s sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

func attrBool(s sdktrace.ReadOnlySpan, key string) (bool, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsBool(), true
		}
	}
	return false, false
}

// TestEmitHooksProduceLoaderShape drives the real singleflight emit (one executor
// caller + call_exec + publishResult + one joiner) and asserts the emitted
// span/link attributes carry exactly the op-kind, dag.digest, UI hints and wait-edge
// (purpose/reason/decimal-ns/target) shape the offline analyzer consumes.
func TestEmitHooksProduceLoaderShape(t *testing.T) {
	const digest = "xxh3:0123456789abcdef"
	const class = "Container.stdout"

	sr, rootCtx, root := newRecordingRoot("POST /query")

	// executor's caller span (the AroundFunc span), then the call_exec under it.
	callerCtx, caller := Tracer(rootCtx).Start(rootCtx, class)
	execCtx, execSpan := beginOTelCallExec(callerCtx, digest, class)
	// publication brackets initCompletedResult under the call_exec context.
	pubSpan := beginOTelPublishResult(execCtx)

	// executor waits on its own call_exec; a joiner (on the caller span) waits too.
	base := time.Now().UnixNano()
	EmitOTelWait(callerCtx, execSpan.SpanContext(), wcprof.WaitReasonCallExec, base, base+2_000_000)
	EmitOTelWait(callerCtx, execSpan.SpanContext(), wcprof.WaitReasonSingleflight, base+500_000, base+2_000_000)

	pubSpan.End()
	execSpan.End()
	caller.End()
	root.End()
	ended := sr.Ended()

	// (1) direct shape assertions — the exact keys/values the fixtures assume.
	exec := spanByKind(t, ended, wcprof.OpKindCallExec.String())
	if exec.Name() != class {
		t.Fatalf("call_exec span name should be the call class %q, got %q", class, exec.Name())
	}
	if got, _ := attrString(exec, telemetry.DagDigestAttr); got != digest {
		t.Fatalf("call_exec dag.digest = %q, want %q", got, digest)
	}
	if pass, ok := attrBool(exec, telemetry.UIPassthroughAttr); !ok || !pass {
		t.Fatalf("call_exec must be ui.passthrough")
	}

	pub := spanByName(t, ended, publishResultSpanName)
	if got, _ := attrString(pub, telemetryattrs.WcprofOpKindAttr); got != wcprof.OpKindInternal.String() {
		t.Fatalf("publishResult op kind = %q, want internal", got)
	}
	// Internal and Passthrough preserve the UI and loader semantics; live start
	// emission is uniform for all spans.
	if internal, ok := attrBool(pub, telemetry.UIInternalAttr); !ok || !internal {
		t.Fatalf("publishResult must be ui.internal")
	}
	if pass, ok := attrBool(pub, telemetry.UIPassthroughAttr); !ok || !pass {
		t.Fatalf("publishResult must be ui.passthrough")
	}

	// the caller span (not the like-named call_exec) carries the wait links.
	var callerSpan sdktrace.ReadOnlySpan
	for _, s := range ended {
		if s.SpanContext().SpanID() == caller.SpanContext().SpanID() {
			callerSpan = s
		}
	}
	if callerSpan == nil {
		t.Fatal("caller span not exported")
	}
	links := callerSpan.Links()
	if len(links) != 2 {
		t.Fatalf("want 2 wait links on the waiter, got %d", len(links))
	}
	for _, l := range links {
		la := map[string]string{}
		for _, kv := range l.Attributes {
			la[string(kv.Key)] = kv.Value.AsString()
		}
		if la[telemetry.LinkPurposeAttr] != telemetryattrs.LinkPurposeWait {
			t.Fatalf("wait link missing %s=wait: %v", telemetry.LinkPurposeAttr, la)
		}
		switch la[telemetryattrs.WcprofWaitReasonAttr] {
		case wcprof.WaitReasonCallExec.String(), wcprof.WaitReasonSingleflight.String():
		default:
			t.Fatalf("unexpected wait reason %q", la[telemetryattrs.WcprofWaitReasonAttr])
		}
		if _, err := strconv.ParseInt(la[telemetryattrs.WcprofWaitStartUnixNanoAttr], 10, 64); err != nil {
			t.Fatalf("wait start must be a decimal-string abs-ns: %v", la)
		}
		if _, err := strconv.ParseInt(la[telemetryattrs.WcprofWaitEndUnixNanoAttr], 10, 64); err != nil {
			t.Fatalf("wait end must be a decimal-string abs-ns: %v", la)
		}
		if l.SpanContext.SpanID() != execSpan.SpanContext().SpanID() {
			t.Fatalf("wait link must target the call_exec span")
		}
	}
}

// TestEmitWaitGateObservableOnMissingTarget covers review finding A: a *recording*
// waiter that joins an execution whose call_exec span was never minted (a mixed /
// cross-session untraced executor) must NOT drop its wait edge silently. It emits a
// targetless wait link — an all-zero target span id — that the offline gate counts
// as an unresolved wait and fails loud on, mirroring native's targetless
// wcprof.BeginWait. Here we assert the emit shape (the observable link is present
// with a zero target); the gate's reaction to it is covered in the analyzer's tests.
func TestEmitWaitGateObservableOnMissingTarget(t *testing.T) {
	sr, rootCtx, root := newRecordingRoot("POST /query")
	base := time.Now().UnixNano()
	// invalid target = the executor minted no call_exec span (oc.execSpanCtx zero).
	EmitOTelWait(rootCtx, trace.SpanContext{}, wcprof.WaitReasonSingleflight, base, base+1_000_000)
	root.End()
	ended := sr.Ended()

	rootSpan := spanByName(t, ended, "POST /query")
	if len(rootSpan.Links()) != 1 {
		t.Fatalf("a missing target on a recording waiter must still emit a gate-observable wait link, got %d links", len(rootSpan.Links()))
	}
	link := rootSpan.Links()[0]
	la := map[string]string{}
	for _, kv := range link.Attributes {
		la[string(kv.Key)] = kv.Value.AsString()
	}
	if la[telemetry.LinkPurposeAttr] != telemetryattrs.LinkPurposeWait {
		t.Fatalf("emitted link must be a wait link: %v", la)
	}
	// The target is deliberately all-zero so the offline gate flags it as an
	// unresolved wait rather than silently dropping the edge.
	if link.SpanContext.SpanID().IsValid() {
		t.Fatalf("a missing-target wait must emit an all-zero target span id, got %s", link.SpanContext.SpanID())
	}
}

// TestEmitWaitNonRecordingWaiterDrops confirms the telemetry-off path stays a
// no-op: with no recording waiter span there is no op in the graph to attribute
// the wait to, so nothing is emitted (and the call must not panic).
func TestEmitWaitNonRecordingWaiterDrops(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.NeverSample()),
		sdktrace.WithSpanProcessor(sr),
	)
	// a NeverSample span is non-recording.
	ctx, span := tp.Tracer("wcprof-otel-test").Start(context.Background(), "untraced")
	if span.IsRecording() {
		t.Fatal("precondition: NeverSample span should be non-recording")
	}
	// Use a valid target to prove it is the waiter's non-recording state, not a
	// missing target, that suppresses emission.
	target := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01},
		SpanID:     trace.SpanID{0x02},
		TraceFlags: trace.FlagsSampled,
	})
	EmitOTelWait(ctx, target, wcprof.WaitReasonSingleflight, 1, 2)
	span.End()
	if got := len(sr.Ended()); got != 0 {
		t.Fatalf("non-recording waiter must export no span/link, got %d ended", got)
	}
}
