package dagql

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

// recordedOps reads the recorder's dump: its ops of the given class recorded
// after the op after, which the test began first, by kind, and each op's
// span event. The recorder outlives a test, and op IDs only increase.
func recordedOps(t *testing.T, class string, after uint64) (map[string][]wcprof.DumpEvent, map[uint64]wcprof.DumpEvent) {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	ops := map[string][]wcprof.DumpEvent{}
	spans := map[uint64]wcprof.DumpEvent{}
	for _, ev := range events {
		switch ev.Type {
		case "op":
			if ev.OpID > after {
				ops[ev.OpKind] = append(ops[ev.OpKind], ev)
			}
		case "span":
			spans[ev.OpID] = ev
		}
	}
	for kind, evs := range ops {
		var keep []wcprof.DumpEvent
		for _, ev := range evs {
			if header.Strings[ev.ClassID] == class || (kind == wcprof.OpKindInternal.String() && header.Strings[ev.ClassID] == publishResultSpanName) {
				keep = append(keep, ev)
			}
		}
		ops[kind] = keep
	}
	return ops, spans
}

func requireOpSpan(t *testing.T, spans map[uint64]wcprof.DumpEvent, op wcprof.DumpEvent, want trace.SpanContext) {
	t.Helper()
	got, ok := spans[op.OpID]
	require.True(t, ok, "op %d (%s) recorded no span", op.OpID, op.OpKind)
	require.Equal(t, want.TraceID().String(), got.TraceID, "op %d (%s) trace", op.OpID, op.OpKind)
	require.Equal(t, want.SpanID().String(), got.SpanID, "op %d (%s) span", op.OpID, op.OpKind)
}

func otelSpanOfKind(t *testing.T, ended []sdktrace.ReadOnlySpan, kind, name string) trace.SpanContext {
	t.Helper()
	for _, s := range ended {
		for _, kv := range s.Attributes() {
			if string(kv.Key) == telemetryattrs.WcprofOpKindAttr && kv.Value.AsString() == kind && (name == "" || s.Name() == name) {
				return s.SpanContext()
			}
		}
	}
	t.Fatalf("no %s span %q", kind, name)
	return trace.SpanContext{}
}

// TestNativeOpsRecordTheirOTelSpans: through the cache's real call and lazy
// paths, with both sources on, each native op the engine emits a span for
// records that span: the call's execution, its result's publication and its
// lazy evaluation. The call itself, run under a span that isn't its own,
// records none.
func TestNativeOpsRecordTheirOTelSpans(t *testing.T) {
	sr, tr := newLazySkipRecorder()
	wcprof.EnsureRecorder()
	profCtx, testOp := wcprof.BeginOp(wcprof.ContextWithProfiling(t.Context()), wcprof.OpKindInternal, "test", wcprof.OpOpts{})
	defer testOp.End(wcprof.OutcomeOK)
	baseCtx := cacheTestContext(profCtx)
	c, err := NewCache(baseCtx, "", nil, nil)
	require.NoError(t, err)
	baseCtx = ContextWithCache(baseCtx, c)
	srv := cacheTestServer(t)
	sessionID := cacheTestSessionID(t, baseCtx)

	callerCtx, caller := tr.Start(baseCtx, "POST /query")
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "op-span-test",
	}
	resAny, err := c.GetOrInitCall(callerCtx, sessionID, srv, &CallRequest{ResultCall: frame},
		func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
				Value:    1,
				lazyEval: func(context.Context) error { return nil },
			}), nil
		})
	require.NoError(t, err)
	require.NoError(t, c.Evaluate(callerCtx, resAny.(ObjectResult[*cacheTestObject])))
	caller.End()
	ended := sr.Ended()

	ops, spans := recordedOps(t, "Query.op-span-test", testOp.ID())
	require.Len(t, ops[wcprof.OpKindCallExec.String()], 1)
	exec := ops[wcprof.OpKindCallExec.String()][0]
	requireOpSpan(t, spans, exec, otelSpanOfKind(t, ended, wcprof.OpKindCallExec.String(), ""))
	require.Len(t, ops[wcprof.OpKindLazy.String()], 1)
	requireOpSpan(t, spans, ops[wcprof.OpKindLazy.String()][0], otelSpanOfKind(t, ended, wcprof.OpKindLazy.String(), ""))
	var pub *wcprof.DumpEvent
	for _, ev := range ops[wcprof.OpKindInternal.String()] {
		if ev.ParentID == exec.OpID {
			pub = &ev
		}
	}
	require.NotNil(t, pub, "no publishResult op under the call's execution")
	requireOpSpan(t, spans, *pub, otelSpanOfKind(t, ended, wcprof.OpKindInternal.String(), publishResultSpanName))
	require.Len(t, ops[wcprof.OpKindCall.String()], 1)
	_, ok := spans[ops[wcprof.OpKindCall.String()][0].OpID]
	require.False(t, ok, "a call run under its caller's span records no span of its own")
}

// TestCallRecordsItsOwnSpan: a call the server's telemetry gave its own span
// records that span; one it didn't records none, even nested under one that
// did.
func TestCallRecordsItsOwnSpan(t *testing.T) {
	sr, tr := newLazySkipRecorder()
	wcprof.EnsureRecorder()
	c, err := NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.Close(context.Background())) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	Fields[*persistCodecRoot]{
		// spanned's resolver makes a nested call, which has no span of its
		// own and runs under spanned's
		Func("spanned", func(ctx context.Context, _ *persistCodecRoot, _ struct{}) (Int, error) {
			if _, err := srv.root.Select(ctx, srv, Selector{Field: "nested"}); err != nil {
				return 0, err
			}
			return NewInt(1), nil
		}),
		Func("nested", func(context.Context, *persistCodecRoot, struct{}) (Int, error) { return NewInt(3), nil }),
		Func("unspanned", func(context.Context, *persistCodecRoot, struct{}) (Int, error) { return NewInt(2), nil }),
	}.Install(srv)
	srv.Around(func(ctx context.Context, req *CallRequest) (context.Context, func(AnyResult, bool, *error)) {
		if req.Field != "spanned" {
			return ctx, NoopDone
		}
		ctx, span := tr.Start(ctx, "Query.spanned")
		return ctx, func(AnyResult, bool, *error) { span.End() }
	})
	ctx := persistDecodeOwnerContext(t, c, srv, "own-span-session")
	ctx, testOp := wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindInternal, "test", wcprof.OpOpts{})
	defer testOp.End(wcprof.OutcomeOK)
	ctx, caller := tr.Start(ctx, "POST /query")
	_, err = srv.root.Select(ctx, srv, Selector{Field: "spanned"})
	require.NoError(t, err)
	_, err = srv.root.Select(ctx, srv, Selector{Field: "unspanned"})
	require.NoError(t, err)
	caller.End()

	var spanned trace.SpanContext
	for _, s := range sr.Ended() {
		if s.Name() == "Query.spanned" {
			spanned = s.SpanContext()
		}
	}
	require.True(t, spanned.IsValid())
	ops, spans := recordedOps(t, "Query.spanned", testOp.ID())
	require.Len(t, ops[wcprof.OpKindCall.String()], 1)
	requireOpSpan(t, spans, ops[wcprof.OpKindCall.String()][0], spanned)
	for _, class := range []string{"Query.unspanned", "Query.nested"} {
		ops, spans = recordedOps(t, class, testOp.ID())
		require.Len(t, ops[wcprof.OpKindCall.String()], 1, class)
		_, ok := spans[ops[wcprof.OpKindCall.String()][0].OpID]
		require.False(t, ok, "%s: a call without its own span records none, not the span it runs under", class)
	}
}

// TestLazySpanStartsWithItsNativeOp: a lazy evaluation's span starts where
// its native op starts, not after the evaluation's span preparation.
func TestLazySpanStartsWithItsNativeOp(t *testing.T) {
	sr, tr := newLazySkipRecorder()
	wcprof.EnsureRecorder()
	profCtx, testOp := wcprof.BeginOp(wcprof.ContextWithProfiling(t.Context()), wcprof.OpKindInternal, "test", wcprof.OpOpts{})
	defer testOp.End(wcprof.OutcomeOK)
	baseCtx := cacheTestContext(profCtx)
	c, err := NewCache(baseCtx, "", nil, nil)
	require.NoError(t, err)
	baseCtx = ContextWithCache(baseCtx, c)
	srv := cacheTestServer(t)
	callerCtx, caller := tr.Start(baseCtx, "POST /query")
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "lazy-start-test",
	}
	resAny, err := c.GetOrInitCall(callerCtx, cacheTestSessionID(t, baseCtx), srv, &CallRequest{ResultCall: frame},
		func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
				Value:    1,
				lazyEval: func(context.Context) error { return nil },
			}), nil
		})
	require.NoError(t, err)
	require.NoError(t, c.Evaluate(callerCtx, resAny.(ObjectResult[*cacheTestObject])))
	caller.End()

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	spans := map[uint64]wcprof.DumpEvent{}
	var lazy *wcprof.DumpEvent
	for i, ev := range events {
		switch {
		case ev.Type == "span":
			spans[ev.OpID] = ev
		case ev.Type == "op" && ev.OpID > testOp.ID() && ev.OpKind == wcprof.OpKindLazy.String() && header.Strings[ev.ClassID] == "Query.lazy-start-test":
			lazy = &events[i]
		}
	}
	require.NotNil(t, lazy)
	sev, ok := spans[lazy.OpID]
	require.True(t, ok, "the lazy op recorded no span")
	for _, s := range sr.Ended() {
		if s.SpanContext().SpanID().String() == sev.SpanID {
			require.Equal(t, header.EpochUnixNano+lazy.StartNS, s.StartTime().UnixNano(), "the lazy span starts apart from its native op")
			return
		}
	}
	t.Fatal("the lazy op's span wasn't exported")
}

// TestCallOpCoversTelemetryCompletion: a call's native op ends after the
// server's telemetry has completed the call (the span's attributes and
// end), which runs on the caller's goroutine, so that work is the call's
// and not its parent's. A call nested under it ends as before.
func TestCallOpCoversTelemetryCompletion(t *testing.T) {
	const hold = 50 * time.Millisecond
	wcprof.EnsureRecorder()
	c, err := NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.Close(context.Background())) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	Fields[*persistCodecRoot]{
		Func("completed", func(ctx context.Context, _ *persistCodecRoot, _ struct{}) (Int, error) {
			if _, err := srv.root.Select(ctx, srv, Selector{Field: "inner"}); err != nil {
				return 0, err
			}
			return NewInt(1), nil
		}),
		Func("inner", func(context.Context, *persistCodecRoot, struct{}) (Int, error) { return NewInt(2), nil }),
	}.Install(srv)
	srv.Around(func(ctx context.Context, req *CallRequest) (context.Context, func(AnyResult, bool, *error)) {
		if req.Field != "completed" {
			return ctx, NoopDone
		}
		return ctx, func(AnyResult, bool, *error) { time.Sleep(hold) }
	})
	ctx := persistDecodeOwnerContext(t, c, srv, "completion-session")
	ctx, testOp := wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindInternal, "test", wcprof.OpOpts{})
	_, err = srv.root.Select(ctx, srv, Selector{Field: "completed"})
	require.NoError(t, err)
	testOp.End(wcprof.OutcomeOK)

	ops, _ := recordedOps(t, "Query.completed", testOp.ID())
	require.Len(t, ops[wcprof.OpKindCall.String()], 1)
	call := ops[wcprof.OpKindCall.String()][0]
	require.GreaterOrEqual(t, call.EndNS-call.StartNS, int64(hold), "the call op ends before its telemetry's completion")
	require.Equal(t, wcprof.OutcomeExecuted.String(), call.Outcome)
	ops, _ = recordedOps(t, "Query.inner", testOp.ID())
	require.Len(t, ops[wcprof.OpKindCall.String()], 1)
	inner := ops[wcprof.OpKindCall.String()][0]
	require.Less(t, inner.EndNS-inner.StartNS, int64(hold), "the nested call took its parent's completion")
}

// TestCallOpStartsWithItsSpan: a call's native op starts where the server's
// telemetry started its span, so the work between them (the telemetry's
// preparation, here a hold) is the call's, as its span already has it.
func TestCallOpStartsWithItsSpan(t *testing.T) {
	const hold = 5 * time.Millisecond
	sr, tr := newLazySkipRecorder()
	wcprof.EnsureRecorder()
	c, err := NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.Close(context.Background())) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	Fields[*persistCodecRoot]{
		Func("started", func(context.Context, *persistCodecRoot, struct{}) (Int, error) { return NewInt(1), nil }),
	}.Install(srv)
	srv.Around(func(ctx context.Context, req *CallRequest) (context.Context, func(AnyResult, bool, *error)) {
		ctx, span := tr.Start(ctx, "Query.started")
		time.Sleep(hold)
		return ctx, func(AnyResult, bool, *error) { span.End() }
	})
	ctx := persistDecodeOwnerContext(t, c, srv, "start-session")
	ctx, testOp := wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindInternal, "test", wcprof.OpOpts{})
	_, err = srv.root.Select(ctx, srv, Selector{Field: "started"})
	require.NoError(t, err)
	testOp.End(wcprof.OutcomeOK)

	var span sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.Name() == "Query.started" {
			span = s
		}
	}
	require.NotNil(t, span)
	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	for _, ev := range events {
		if ev.Type == "op" && ev.OpID > testOp.ID() && ev.OpKind == wcprof.OpKindCall.String() && header.Strings[ev.ClassID] == "Query.started" {
			// the op's start is the span's on the recorder's monotonic clock,
			// so read back on the wall clock it can be nanoseconds apart
			require.InDelta(t, span.StartTime().UnixNano(), header.EpochUnixNano+ev.StartNS, float64(time.Microsecond), "the call op starts apart from its span")
			return
		}
	}
	t.Fatal("no call op")
}
