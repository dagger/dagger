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
	state      string
	waitingOn  string
	stopReason string
	digest     string
	body       string
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
