package core

// The agent state channel is edge-triggered on the PROJECTION, which is the
// part most likely to regress silently: transitionLocked fires on every fact
// change, and most fact changes do not move the state, so a publication hung
// off the wrong hook would flood every client with duplicates — or, worse,
// coalesce two real transitions into one and leave a roster lying.

import (
	"context"
	"sync"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// recordedState is one published agent-state record, flattened to the fields
// the directory contract promises.
type recordedState struct {
	state     string
	waitingOn string
	body      string
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

func (r *stateRecorder) states() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	states := make([]string, len(r.records))
	for i, rec := range r.records {
		states[i] = rec.state
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

func (rt *AgentRuntime) testTransition(mut func()) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.transitionLocked(mut)
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

	EmitAgentState(ctx, AgentStateWaitingInput, "ok to delete testdata/legacy?")
	EmitAgentState(ctx, AgentStateRunning, "")

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
