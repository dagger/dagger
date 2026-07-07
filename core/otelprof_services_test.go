package core

// Emit-path regression test for the service start emit, mirroring
// dagql's otelprof_hooks_test.go discipline: drive the REAL emit helpers
// (beginOTelServiceStart + dagql.EmitOTelWait) against an in-memory SDK tracer and
// assert the genuinely-exported service.start span and the installer wait edge
// carry exactly the op-kind, digest, passthrough and wait (purpose/reason/target/
// abs-ns) shape the offline analyzer consumes — so the emit contract is
// machine-checked, not just code-reviewed. (The offline loader/gate that consume
// this shape live in the closed-source analyzer; their end-to-end coverage — the
// wait resolving to the service.start op — is exercised there via
// fixtures.)

import (
	"context"
	"strconv"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

func otelprofRecordingRoot(name string) (*tracetest.SpanRecorder, context.Context, trace.Span) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sr),
	)
	ctx, root := tp.Tracer("wcprof-otel-test").Start(context.Background(), name)
	return sr, ctx, root
}

func otelprofSpanByName(t *testing.T, ended []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range ended {
		if s.Name() == name {
			return s
		}
	}
	t.Fatalf("no exported span named %q", name)
	return nil
}

func otelprofAttrStr(s sdktrace.ReadOnlySpan, key string) string {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsString()
		}
	}
	return ""
}

func otelprofAttrBool(s sdktrace.ReadOnlySpan, key string) bool {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsBool()
		}
	}
	return false
}

// TestEmitServiceStartProducesLoaderShape drives the real service-start emit (a
// first installer that mints + ends the service.start span, plus a joining
// installer that blocks on it and emits a service wait edge) and asserts the
// genuinely-exported service.start span and the wait link carry exactly the
// op-kind/digest/passthrough and wait (purpose/reason/target/abs-ns) shape the
// offline analyzer consumes.
func TestEmitServiceStartProducesLoaderShape(t *testing.T) {
	const digest = "sha256:service-digest"

	sr, ctx, root := otelprofRecordingRoot("POST /query")

	// installer1 runs the start: service.start is minted under its span (svcCtx
	// carries the installer span in startWithKey).
	inst1Ctx, inst1 := Tracer(ctx).Start(ctx, "Container.asService")
	_, startSpan := beginOTelServiceStart(inst1Ctx, digest)

	// installer2 joins the in-flight start and blocks on it, crediting the blocked
	// interval to the service.start span. A small real sleep keeps the wait window
	// within the live spans' timeframe (no negative rebased times).
	inst2Ctx, inst2 := Tracer(ctx).Start(ctx, "Container.withServiceBinding")
	waitStart := time.Now().UnixNano()
	time.Sleep(3 * time.Millisecond)
	waitEnd := time.Now().UnixNano()
	dagql.EmitOTelWait(inst2Ctx, startSpan.SpanContext(), wcprof.WaitReasonService, waitStart, waitEnd)

	var nilErr error
	endOTelServiceStart(startSpan, &nilErr)
	inst2.End()
	inst1.End()
	root.End()
	ended := sr.Ended()

	// (1) service.start span shape.
	start := otelprofSpanByName(t, ended, "service.start")
	if got := otelprofAttrStr(start, telemetryattrs.WcprofOpKindAttr); got != wcprof.OpKindServiceStart.String() {
		t.Fatalf("service.start op kind = %q, want service_start", got)
	}
	if got := otelprofAttrStr(start, telemetry.DagDigestAttr); got != digest {
		t.Fatalf("service.start dag.digest = %q, want %q", got, digest)
	}
	if !otelprofAttrBool(start, telemetry.UIPassthroughAttr) {
		t.Fatal("service.start must be ui.passthrough (the visible service span is the long-lived exec span)")
	}

	// (2) the joining installer carries the wait edge to service.start.
	var inst2Span sdktrace.ReadOnlySpan
	for _, s := range ended {
		if s.SpanContext().SpanID() == inst2.SpanContext().SpanID() {
			inst2Span = s
		}
	}
	if inst2Span == nil {
		t.Fatal("installer2 span not exported")
	}
	if len(inst2Span.Links()) != 1 {
		t.Fatalf("the blocked installer must carry exactly 1 service wait link, got %d", len(inst2Span.Links()))
	}
	link := inst2Span.Links()[0]
	la := map[string]string{}
	for _, kv := range link.Attributes {
		la[string(kv.Key)] = kv.Value.AsString()
	}
	if la[telemetry.LinkPurposeAttr] != telemetryattrs.LinkPurposeWait {
		t.Fatalf("service wait link missing %s=wait: %v", telemetry.LinkPurposeAttr, la)
	}
	if la[telemetryattrs.WcprofWaitReasonAttr] != wcprof.WaitReasonService.String() {
		t.Fatalf("service wait reason = %q, want service", la[telemetryattrs.WcprofWaitReasonAttr])
	}
	if link.SpanContext.SpanID() != startSpan.SpanContext().SpanID() {
		t.Fatal("the service wait link must target the service.start span")
	}
	// the wait carries well-formed abs-ns timing (what the offline gate's
	// MalformedWaitTimings check consumes): decimal-string start ≤ end.
	ws, err := strconv.ParseInt(la[telemetryattrs.WcprofWaitStartUnixNanoAttr], 10, 64)
	if err != nil {
		t.Fatalf("wait start must be a decimal-string abs-ns: %v", la)
	}
	we, err := strconv.ParseInt(la[telemetryattrs.WcprofWaitEndUnixNanoAttr], 10, 64)
	if err != nil {
		t.Fatalf("wait end must be a decimal-string abs-ns: %v", la)
	}
	if ws > we {
		t.Fatalf("wait start %d must not exceed end %d", ws, we)
	}
}
