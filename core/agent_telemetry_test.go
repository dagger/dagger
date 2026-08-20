package core

// The agent state channel is edge-triggered on the PROJECTION, which is the
// part most likely to regress silently: transitionLocked fires on every fact
// change, and most fact changes do not move the state, so a publication hung
// off the wrong hook would flood every client with duplicates — or, worse,
// coalesce two real transitions into one and leave a roster lying.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// recordedState is one published agent-state record, flattened to the fields
// the directory contract promises.
type recordedState struct {
	state       string
	waitingOn   string
	stopReason  string
	digest      string
	body        string
	contentType string
}

// stateRecorder captures agent state records emitted through a context.
type stateRecorder struct {
	mu      sync.Mutex
	records []recordedState
}

func (r *stateRecorder) OnEmit(ctx context.Context, rec *sdklog.Record) error {
	got := recordedState{body: rec.Body().AsString()}
	rec.WalkAttributes(func(kv otellog.KeyValue) bool {
		switch kv.Key {
		case telemetryattrs.AgentStateAttr:
			got.state = kv.Value.AsString()
		case telemetryattrs.AgentWaitingOnAttr:
			got.waitingOn = kv.Value.AsString()
		case telemetryattrs.AgentStopReasonAttr:
			got.stopReason = kv.Value.AsString()
		case telemetryattrs.AgentSnapshotDigestAttr:
			got.digest = kv.Value.AsString()
		case telemetry.ContentTypeAttr:
			got.contentType = kv.Value.AsString()
		}
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, got)
	return nil
}

func (r *stateRecorder) Shutdown(context.Context) error   { return nil }
func (r *stateRecorder) ForceFlush(context.Context) error { return nil }

func (r *stateRecorder) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }

// states lists the state records only: a snapshot record carries no state, and
// the two channels are deliberately separate (a commit is not a transition).
func (r *stateRecorder) states() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var states []string
	for _, rec := range r.records {
		if rec.state == "" {
			continue
		}
		states = append(states, rec.state)
	}
	return states
}

// stateRecorderCtx returns a context whose logger provider records every
// agent state record emitted through it.
func stateRecorderCtx(t *testing.T) (*stateRecorder, context.Context) {
	t.Helper()
	rec := &stateRecorder{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(rec))
	return rec, telemetry.WithLoggerProvider(context.Background(), provider)
}

// testRuntime builds a bare runtime entry wired to a recording context — the
// facts and the publication plumbing only, since that is all the state
// projection reads.
func testRuntime(ctx context.Context) *AgentRuntime {
	return &AgentRuntime{
		name:         "test",
		stateChanged: make(chan struct{}),
		spanCtx:      ctx,
	}
}

func testAgentContext(t *testing.T, ctx context.Context, id, name string) context.Context {
	t.Helper()
	srv, err := dagql.NewServer(ctx, &Query{})
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Agent]{Typed: &Agent{}}))
	agent := &Agent{InstanceID: id, Name: name}
	res, err := dagql.NewObjectResultForCall(agent, srv, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "test_agent",
		Type:        dagql.NewResultCallType(agent.Type()),
	})
	require.NoError(t, err)
	return AgentToContext(ctx, res)
}

func (rt *AgentRuntime) testTransition(mut func()) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.transitionLocked(mut)
}

func TestAgentTombstoneTelemetryContextIsCompact(t *testing.T) {
	recorder, ctx := stateRecorderCtx(t)
	loggerProvider := telemetry.LoggerProvider(ctx)

	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
		Remote:  true,
	})
	ctx = trace.ContextWithSpanContext(ctx, spanContext)
	ctx = ContextWithQuery(ctx, &Query{})

	lease := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "request", func() {}, nil)
	defer lease.Release()
	scope, err := engine.NewClientScope(&engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "client",
	}, lease)
	require.NoError(t, err)
	ctx, err = engine.ContextWithClientScope(ctx, scope)
	require.NoError(t, err)

	compact := agentTombstoneTelemetryContext(ctx)
	_, err = CurrentQuery(compact)
	require.Error(t, err, "tombstones must not retain the runtime query")
	_, ok := engine.ClientScopeFromContext(compact)
	require.False(t, ok, "the durable lease is stored explicitly, not in spanCtx")
	metadata, err := engine.ClientMetadataFromContext(compact)
	require.NoError(t, err)
	require.Equal(t, "session", metadata.SessionID)
	require.Equal(t, "client", metadata.ClientID)
	require.Same(t, loggerProvider, telemetry.LoggerProvider(compact))
	require.Equal(t, spanContext, trace.SpanContextFromContext(compact))
	require.Nil(t, dagql.CurrentDagqlServer(compact))

	EmitAgentState(compact, AgentStateStopped, "", AgentStopExplicit)
	EmitAgentSnapshot(compact, "xxh3:compact")
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	require.Len(t, recorder.records, 2)
	require.Equal(t, string(AgentStateStopped), recorder.records[0].state)
	require.Equal(t, "xxh3:compact", recorder.records[1].digest)
}

func TestAgentRetainsLaunchingScopeThroughTombstoneUntilTeardown(t *testing.T) {
	t.Parallel()

	var acquired atomic.Int32
	var released atomic.Int32
	rootLease := engine.NewClientLifecycleLease(
		engine.ClientLeaseRequest,
		"request",
		func() {},
		func(kind engine.ClientLeaseKind, ownerID string) (*engine.ClientLifecycleLease, error) {
			require.Equal(t, engine.ClientLeaseAgent, kind)
			require.Equal(t, "agent-scope", ownerID)
			acquired.Add(1)
			return engine.NewClientLifecycleLease(kind, ownerID, func() { released.Add(1) }, nil), nil
		},
	)
	scope, err := engine.NewClientScope(&engine.ClientMetadata{SessionID: "session", ClientID: "client"}, rootLease)
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(context.Background(), scope)
	require.NoError(t, err)
	ctx = ContextWithQuery(ctx, &Query{})
	ctx = testAgentContext(t, ctx, "agent-scope", "leased")
	agent, ok := AgentFromContext(ctx)
	require.True(t, ok)

	runtimes := NewAgentRuntimes()
	runtime, err := runtimes.GetOrCreate(ctx, agent)
	require.NoError(t, err)
	// Let the loop launch and immediately take its clean terminal path. The
	// snapshot still embeds DagQL state, so the tombstone must retain its scope.
	runtime.mu.Lock()
	runtime.stopRequested = true
	runtime.mu.Unlock()
	_, err = runtimes.Start(ctx, agent)
	require.NoError(t, err)
	require.NoError(t, runtime.WaitFor(ctx, AgentStateStopped))
	require.Equal(t, int32(1), acquired.Load())
	require.Zero(t, released.Load())
	runtime.mu.Lock()
	tombstoneCtx := runtime.spanCtx
	durableLease := runtime.scopeLease
	runtime.mu.Unlock()
	_, err = CurrentQuery(tombstoneCtx)
	require.Error(t, err, "ordinary loop tombstones must compact resolver context")
	_, ok = engine.ClientScopeFromContext(tombstoneCtx)
	require.False(t, ok)
	require.True(t, durableLease.Held(), "snapshot retention still requires its explicit lease")

	require.NoError(t, runtimes.KillAll(ctx, errors.New("session closed")))
	require.Equal(t, int32(1), released.Load())
}

// TestPublishStateEdgeTriggered covers the core contract: one record per
// change of the projected state, and none at all for fact changes that leave
// the projection where it was.
func TestPublishStateEdgeTriggered(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(ctx)

	// Seed, as loop() does on start.
	rt.mu.Lock()
	rt.publishStateLocked()
	rt.mu.Unlock()
	require.Equal(t, []string{string(AgentStateIdle)}, rec.states())

	// Mail arrives: IDLE -> RUNNING.
	rt.testTransition(func() { rt.mailbox = append(rt.mailbox, "msg-1") })
	require.Equal(t, []string{"IDLE", "RUNNING"}, rec.states())

	// The turn opens and a step starts, draining the mailbox. Three more
	// fact changes, all of which project RUNNING: nothing new is published.
	rt.testTransition(func() { rt.turnOpen = true })
	rt.testTransition(func() { rt.mailbox = nil })
	rt.testTransition(func() { rt.stepping = true })
	require.Equal(t, []string{"IDLE", "RUNNING"}, rec.states(),
		"fact changes that leave the projection alone must not publish")

	// The step lands and the turn resolves: back to IDLE.
	rt.testTransition(func() { rt.stepping = false; rt.turnOpen = false })
	require.Equal(t, []string{"IDLE", "RUNNING", "IDLE"}, rec.states())

	// A pause mid-idle is a real transition, and so is the resume.
	rt.testTransition(func() { rt.paused = true })
	rt.testTransition(func() { rt.paused = false })
	require.Equal(t, []string{"IDLE", "RUNNING", "IDLE", "PAUSED", "IDLE"}, rec.states())
}

// TestPublishStateBeforeStartIsSilent covers the inert entry: an agent
// created but never started has no span to attribute records to, and its
// absence of records is exactly what a client projects as IDLE anyway.
func TestPublishStateBeforeStartIsSilent(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(ctx)
	rt.spanCtx = nil

	rt.testTransition(func() { rt.mailbox = append(rt.mailbox, "msg-1") })
	require.Empty(t, rec.states(), "an unstarted runtime must publish nothing")

	// Once the loop starts, the first publication reports the state as it
	// stands — including the mail that arrived while it was inert.
	rt.mu.Lock()
	rt.spanCtx = ctx
	rt.publishStateLocked()
	rt.mu.Unlock()
	require.Equal(t, []string{"RUNNING"}, rec.states())
}

// TestPublishStateSealedTombstone covers the transition that outlives the
// loop span: stop sealing a FAILED tombstone runs after loop() returned, and
// must still reach the roster — otherwise a failed agent looks retryable
// forever.
func TestPublishStateSealedTombstone(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(ctx)

	rt.testTransition(func() {
		rt.done = true
		rt.loopErr = context.DeadlineExceeded
	})
	require.Equal(t, []string{"FAILED"}, rec.states())

	rt.testTransition(func() { rt.sealed = true })
	require.Equal(t, []string{"FAILED", "STOPPED"}, rec.states())
}

// TestEmitAgentStateRecordShape locks the wire contract a consumer keys on:
// the state token, the parked question, and an explicitly empty body so the
// record is never mistaken for log text.
func TestEmitAgentStateRecordShape(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)

	EmitAgentState(ctx, AgentStateWaitingInput, "ok to delete testdata/legacy?", "")
	EmitAgentState(ctx, AgentStateRunning, "", "")

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.records, 2)

	require.Equal(t, "WAITING_INPUT", rec.records[0].state)
	require.Equal(t, "ok to delete testdata/legacy?", rec.records[0].waitingOn)
	require.Empty(t, rec.records[0].body, "state records must not carry log text")

	// The question is cleared explicitly rather than omitted, so a consumer
	// folding records latest-wins drops a question that has been answered.
	require.Equal(t, "RUNNING", rec.records[1].state)
	require.Empty(t, rec.records[1].waitingOn)
}

// TestEmitAgentSnapshotRecordShape locks the resume anchor's wire contract:
// the digest of the last committed conversation, on a record of its own, with
// no state token and an explicitly empty body.
//
// A record of its own is forced: state records are edge-triggered on the
// projected state, and most commits do not move the state while every commit
// moves the snapshot — folding the digest into them would publish a resume
// anchor stuck at whatever the conversation was when the agent last changed
// state.
func TestEmitAgentSnapshotRecordShape(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)

	EmitAgentSnapshot(ctx, "xxh3:9e107d9d372bb682")

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.records, 1)
	require.Equal(t, "xxh3:9e107d9d372bb682", rec.records[0].digest)
	require.Empty(t, rec.records[0].state,
		"a commit is not a transition: the snapshot record carries no state")
	require.Empty(t, rec.records[0].body, "snapshot records must not carry log text")
}

// TestEmitAgentFailureMessage locks the durable failure surface: the loop's
// actual error becomes a failed assistant message beneath the loop, with the
// same text on stdio for the focused conversation to retain in scrollback.
func TestEmitAgentFailureMessage(t *testing.T) {
	logs, ctx := stateRecorderCtx(t)
	ctx = testAgentContext(t, ctx, "agent-123", "reviewer")
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(spans),
	)
	ctx, loop := tp.Tracer("agent-failure-test").Start(ctx, "agent loop")
	defer loop.End()

	// A clean stop is silent.
	emitAgentFailure(ctx, nil)
	require.Empty(t, spans.Ended())

	loopErr := errors.New("context limit reached")
	emitAgentFailure(ctx, loopErr)

	require.Len(t, spans.Ended(), 1)
	failure := spans.Ended()[0]
	require.Equal(t, "agent failure", failure.Name())
	require.Equal(t, loop.SpanContext().SpanID(), failure.Parent().SpanID())
	require.Equal(t, codes.Error, failure.Status().Code)
	require.Equal(t, loopErr.Error(), failure.Status().Description)

	attrs := map[string]string{}
	for _, attr := range failure.Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	require.Equal(t, telemetry.LLMRoleAssistant, attrs[telemetry.LLMRoleAttr])
	require.Equal(t, telemetry.UIMessageReceived, attrs[telemetry.UIMessageAttr])
	require.Equal(t, "agent-123", attrs[string(semconv.GenAIAgentIDKey)])
	require.Equal(t, "reviewer", attrs[string(semconv.GenAIAgentNameKey)])

	logs.mu.Lock()
	defer logs.mu.Unlock()
	var bodies []string
	for _, rec := range logs.records {
		if rec.body != "" {
			bodies = append(bodies, rec.body)
		}
	}
	require.Equal(t, []string{loopErr.Error()}, bodies)
}

// TestStopReasonRidesTerminalRecord covers the fact that makes a trace
// restorable at all: a stop somebody asked for and a stop the session's
// teardown performed are the same STOPPED projection, and only the reason
// tells them apart. Restoring the first as live reverses a dismissal;
// refusing to restore the second loses a cleanly closed session entirely.
func TestStopReasonRidesTerminalRecord(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(ctx)

	// A non-terminal transition carries no reason, so a consumer folding
	// records latest-wins never attributes a stale reason to a later stop.
	rt.testTransition(func() { rt.paused = true })

	require.NoError(t, rt.Stop(context.Background(), false, nil, AgentStopExplicit))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.records, 2)
	require.Equal(t, "PAUSED", rec.records[0].state)
	require.Empty(t, rec.records[0].stopReason)
	require.Equal(t, "STOPPED", rec.records[1].state)
	require.Equal(t, string(AgentStopExplicit), rec.records[1].stopReason)
}

// TestKillAllStopsWithSessionReason is the other half: session teardown stops
// every entry it holds, and each says so — otherwise a normal exit publishes a
// trace in which every agent looks deliberately dismissed.
func TestKillAllStopsWithSessionReason(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(ctx)
	rt.key = "instance-1"

	ars := NewAgentRuntimes()
	ars.entries[rt.key] = rt
	require.NoError(t, ars.KillAll(context.Background(), errors.New("session closed")))

	require.Equal(t, []string{"STOPPED"}, rec.states())
	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Equal(t, string(AgentStopSession), rec.records[0].stopReason)
}
