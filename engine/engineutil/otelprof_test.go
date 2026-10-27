package engineutil

// Emit-path regression test for the exec engine/user split,
// mirroring dagql's otelprof_hooks_test.go discipline: drive the REAL emit
// helpers (beginOTelExecRun + emitOTelExecSplit) against an in-memory SDK
// tracer and assert the genuinely-exported spans carry exactly the op-kind,
// work-type, passthrough, parentage, timing and argv shape the offline analyzer
// consumes — so the emit contract is machine-checked, not just code-reviewed.
// (The offline loader/gate that consume this shape live in the closed-source
// analyzer; their end-to-end coverage is exercised there via fixtures.)

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// newRecordingRoot returns an always-sampling in-memory recorder plus a root span
// whose context drives Tracer(ctx) for the exec emit helpers under test.
func newRecordingRoot(name string) (*tracetest.SpanRecorder, context.Context, trace.Span) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(sr),
	)
	ctx, root := tp.Tracer("wcprof-otel-test").Start(context.Background(), name)
	return sr, ctx, root
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

func attrStr(s sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

func attrBool(s sdktrace.ReadOnlySpan, key string) bool {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsBool()
		}
	}
	return false
}

// TestEmitExecSplitProducesLoaderShape drives the real exec.run + split emit (a
// withExec with engine container-setup then a longer user process) and asserts the
// exported spans carry exactly the op-kind, work-type, passthrough, parentage,
// timing and argv shape the offline analyzer consumes — so the split that lets a
// slow user process rank as user work is machine-checked at the emit boundary.
func TestEmitExecSplitProducesLoaderShape(t *testing.T) {
	const (
		digest  = "xxh3:withexec-digest"
		stateID = "exec-state-id-0001"
	)
	sr, ctx, root := newRecordingRoot("Container.withExec")

	// the executor runs under the call_exec span (here the recording root); emit
	// exec.run + the split. ctx carries the parent span, so Tracer(ctx) nests them.
	// Small REAL elapsed time keeps the live exec.run/root spans enclosing the
	// backdated split (as production exec.run brackets the whole run): 4ms engine
	// container-setup, then 12ms user process.
	ctx, execRun := beginOTelExecRun(ctx, digest)
	start := time.Now()
	time.Sleep(4 * time.Millisecond)
	started := time.Now()
	time.Sleep(12 * time.Millisecond)
	end := time.Now()
	emitOTelExecSplit(ctx, stateID, start, started, time.Time{}, end, nil, []string{"go", "build", "./..."}, execPhaseOps{})
	var nilErr error
	endOTelExecRun(execRun, &nilErr)
	root.End()
	ended := sr.Ended()

	// (1) exec.run shape.
	run := spanByName(t, ended, "exec.run")
	if got, _ := attrStr(run, telemetryattrs.WcprofOpKindAttr); got != wcprof.OpKindExec.String() {
		t.Fatalf("exec.run op kind = %q, want exec", got)
	}
	if got, _ := attrStr(run, telemetry.DagDigestAttr); got != digest {
		t.Fatalf("exec.run dag.digest = %q, want %q", got, digest)
	}
	if !attrBool(run, telemetry.UIPassthroughAttr) {
		t.Fatal("exec.run must be ui.passthrough")
	}

	// (2) containerStart (engine): exec_phase, no work_type, child of exec.run,
	// timing [base, started].
	cs := spanByName(t, ended, "exec.containerStart")
	if got, _ := attrStr(cs, telemetryattrs.WcprofOpKindAttr); got != wcprof.OpKindExecPhase.String() {
		t.Fatalf("containerStart op kind = %q, want exec_phase", got)
	}
	if _, ok := attrStr(cs, telemetryattrs.WcprofWorkTypeAttr); ok {
		t.Fatal("containerStart (engine) must NOT carry work_type=user")
	}
	if cs.Parent().SpanID() != run.SpanContext().SpanID() {
		t.Fatal("containerStart must nest under exec.run")
	}
	if !cs.StartTime().Equal(start) || !cs.EndTime().Equal(started) {
		t.Fatalf("containerStart interval = [%v,%v], want [%v,%v]", cs.StartTime(), cs.EndTime(), start, started)
	}

	// (3) processRun (user): exec_phase, work_type=user, child of exec.run, timing
	// [started, end] — the user-work-first-class span.
	pr := spanByName(t, ended, "exec.processRun")
	if got, _ := attrStr(pr, telemetryattrs.WcprofOpKindAttr); got != wcprof.OpKindExecPhase.String() {
		t.Fatalf("processRun op kind = %q, want exec_phase", got)
	}
	if got, ok := attrStr(pr, telemetryattrs.WcprofWorkTypeAttr); !ok || got != wcprof.WorkTypeUser.String() {
		t.Fatalf("processRun must carry work_type=user, got %q (present=%v)", got, ok)
	}
	if !attrBool(pr, telemetry.UIPassthroughAttr) {
		t.Fatal("processRun must be ui.passthrough")
	}
	if pr.Parent().SpanID() != run.SpanContext().SpanID() {
		t.Fatal("processRun must nest under exec.run")
	}
	if !pr.StartTime().Equal(started) || !pr.EndTime().Equal(end) {
		t.Fatalf("processRun interval = [%v,%v], want [%v,%v]", pr.StartTime(), pr.EndTime(), started, end)
	}
	// processRun carries the user command as the scalar JSON-array string the offline
	// analyzer parses into the op's argv (the same bytes native interns); the engine
	// containerStart phase must not.
	if got, ok := attrStr(pr, telemetryattrs.WcprofExecArgvAttr); !ok || got != `["go","build","./..."]` {
		t.Fatalf("processRun must carry wcprof.exec.argv = %q, got %q (present=%v)", `["go","build","./..."]`, got, ok)
	}
	if _, ok := attrStr(cs, telemetryattrs.WcprofExecArgvAttr); ok {
		t.Fatal("containerStart (engine) must NOT carry wcprof.exec.argv")
	}
}

// TestEmitExecSplitSetupFailure covers the never-started case: a setup failure
// before the process starts yields a single containerStart over the whole
// interval, charged the run error, and NO processRun (mirroring native).
func TestEmitExecSplitSetupFailure(t *testing.T) {
	sr, ctx, root := newRecordingRoot("Container.withExec")
	ctx, execRun := beginOTelExecRun(ctx, "xxh3:failed-exec")

	start := time.Now()
	time.Sleep(5 * time.Millisecond)
	end := time.Now()
	runErr := errors.New("setup failed: mount error")
	// started == zero time: the process never started. A non-nil argv is supplied to
	// prove it never leaks onto the engine-setup span when there is no user process.
	emitOTelExecSplit(ctx, "exec-state-id-fail", start, time.Time{}, time.Time{}, end, runErr, []string{"go", "build"}, execPhaseOps{})
	endOTelExecRun(execRun, &runErr)
	root.End()
	ended := sr.Ended()

	for _, s := range ended {
		if s.Name() == "exec.processRun" {
			t.Fatal("a never-started exec must emit no processRun span")
		}
		if _, ok := attrStr(s, telemetryattrs.WcprofExecArgvAttr); ok {
			t.Fatal("a never-started exec must emit no wcprof.exec.argv (no user process ran)")
		}
	}
	cs := spanByName(t, ended, "exec.containerStart")
	if cs.Status().Code != codes.Error {
		t.Fatal("a setup failure must charge the error to containerStart")
	}
	if !cs.StartTime().Equal(start) || !cs.EndTime().Equal(end) {
		t.Fatalf("failed containerStart must span the whole [%v,%v], got [%v,%v]", start, end, cs.StartTime(), cs.EndTime())
	}
}

// TestEmitExecSplitRecordsPhaseSpans: the native exec phases name the phase
// spans emitted for them; a run whose process never started has only its
// container start.
func TestEmitExecSplitRecordsPhaseSpans(t *testing.T) {
	wcprof.EnsureRecorder()
	sr, ctx, root := newRecordingRoot("Container.withExec")
	ctx = wcprof.ContextWithProfiling(ctx)
	start := time.Now()
	started := start.Add(4 * time.Millisecond)
	end := started.Add(12 * time.Millisecond)
	startOp := wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, "exec.containerStart", wcprof.OpOpts{}, 0, 4, wcprof.OutcomeOK)
	runOp := wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, "exec.processRun", wcprof.OpOpts{}, 4, 16, wcprof.OutcomeOK)
	emitOTelExecSplit(ctx, "exec-phase-spans", start, started, time.Time{}, end, nil, nil, execPhaseOps{containerStart: startOp, processRun: runOp})
	failOp := wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, "exec.containerStart", wcprof.OpOpts{}, 0, 4, wcprof.OutcomeError)
	emitOTelExecSplit(ctx, "exec-phase-spans-fail", start, time.Time{}, time.Time{}, end, errors.New("boom"), nil, execPhaseOps{containerStart: failOp})
	root.End()

	var buf bytes.Buffer
	if err := wcprof.Active().WriteDump(&buf, false); err != nil {
		t.Fatal(err)
	}
	_, events, err := wcprof.ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	spanOf := map[uint64]string{}
	for _, ev := range events {
		if ev.Type == "span" {
			spanOf[ev.OpID] = ev.SpanID
		}
	}
	phase := func(name, ident string) string {
		for _, s := range sr.Ended() {
			if v, _ := attrStr(s, telemetry.DagDigestAttr); s.Name() == name && v == ident {
				return s.SpanContext().SpanID().String()
			}
		}
		t.Fatalf("no %s span for %s", name, ident)
		return ""
	}
	for op, want := range map[uint64]string{
		startOp: phase("exec.containerStart", "exec-phase-spans"),
		runOp:   phase("exec.processRun", "exec-phase-spans"),
		failOp:  phase("exec.containerStart", "exec-phase-spans-fail"),
	} {
		if spanOf[op] != want {
			t.Errorf("op %d span = %q, want %q", op, spanOf[op], want)
		}
	}
}

// TestEmitExecSplitRuntimeStart: when runc's release of the workload is
// known, runc creating the container is its own internal exec.runtimeStart
// phase between the container start and the process run, as native records
// it, and names its native op; one released after the run ended is ignored,
// as native ignores it.
func TestEmitExecSplitRuntimeStart(t *testing.T) {
	wcprof.EnsureRecorder()
	sr, ctx, root := newRecordingRoot("Container.withExec")
	ctx = wcprof.ContextWithProfiling(ctx)
	start := time.Now()
	started := start.Add(4 * time.Millisecond)
	released := started.Add(9 * time.Millisecond)
	end := released.Add(12 * time.Millisecond)
	rtOp := wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, "exec.runtimeStart", wcprof.OpOpts{}, 4, 13, wcprof.OutcomeOK)
	emitOTelExecSplit(ctx, "exec-runtime-start", start, started, released, end, nil, []string{"go"}, execPhaseOps{runtimeStart: rtOp})
	emitOTelExecSplit(ctx, "exec-runtime-late", start, started, end.Add(time.Millisecond), end, nil, nil, execPhaseOps{})
	root.End()
	ended := sr.Ended()

	phase := func(name, ident string) sdktrace.ReadOnlySpan {
		for _, s := range ended {
			if v, _ := attrStr(s, telemetry.DagDigestAttr); s.Name() == name && v == ident {
				return s
			}
		}
		return nil
	}
	rt := phase("exec.runtimeStart", "exec-runtime-start")
	if rt == nil {
		t.Fatal("no exec.runtimeStart span")
	}
	if !rt.StartTime().Equal(started) || !rt.EndTime().Equal(released) {
		t.Fatalf("runtimeStart = [%v,%v], want [%v,%v]", rt.StartTime(), rt.EndTime(), started, released)
	}
	if k, _ := attrStr(rt, telemetryattrs.WcprofOpKindAttr); k != wcprof.OpKindExecPhase.String() {
		t.Fatalf("runtimeStart kind = %q, want exec_phase", k)
	}
	if !attrBool(rt, telemetry.UIInternalAttr) || !attrBool(rt, telemetry.UIPassthroughAttr) {
		t.Fatal("runtimeStart must be internal and passthrough")
	}
	if _, ok := attrStr(rt, telemetryattrs.WcprofWorkTypeAttr); ok {
		t.Fatal("runtimeStart is engine work, not user work")
	}
	if pr := phase("exec.processRun", "exec-runtime-start"); pr == nil || !pr.StartTime().Equal(released) || !pr.EndTime().Equal(end) {
		t.Fatalf("processRun must run from the release to the end, got %v", pr)
	}
	if cs := phase("exec.containerStart", "exec-runtime-start"); cs == nil || !cs.EndTime().Equal(started) {
		t.Fatalf("containerStart must end at the started callback, got %v", cs)
	}
	if phase("exec.runtimeStart", "exec-runtime-late") != nil {
		t.Fatal("a release after the run ended must not make a runtimeStart span")
	}
	if pr := phase("exec.processRun", "exec-runtime-late"); pr == nil || !pr.StartTime().Equal(started) {
		t.Fatalf("without a usable release, processRun starts at the started callback, got %v", pr)
	}

	var buf bytes.Buffer
	if err := wcprof.Active().WriteDump(&buf, false); err != nil {
		t.Fatal(err)
	}
	_, events, err := wcprof.ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Type == "span" && ev.OpID == rtOp {
			if ev.SpanID != rt.SpanContext().SpanID().String() {
				t.Fatalf("runtimeStart op span = %s, want %s", ev.SpanID, rt.SpanContext().SpanID())
			}
			return
		}
	}
	t.Fatal("the runtimeStart op recorded no span")
}
