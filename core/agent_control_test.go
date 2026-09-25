package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/stretchr/testify/require"
)

func TestControlPublishesCommittedCallLeaf(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*LLM](srv))
	root := testResultCall("llm", &LLM{}, nil)
	frame := testResultCall("withResponse", &LLM{}, root)
	// No reconstructible in-memory MCP state: publication must use the actual
	// committed call, not synthesize a conversation from the LLM's contents.
	value, err := dagql.NewObjectResultForCall(&LLM{}, srv, frame)
	require.NoError(t, err)
	attached, err := cache.GetOrInitCall(ctx, "committed-leaf", srv,
		&dagql.CallRequest{ResultCall: frame}, dagql.ValueFunc(value))
	require.NoError(t, err)
	committed := attached.(dagql.ObjectResult[*LLM])
	handle, err := committed.ID()
	require.NoError(t, err)
	require.True(t, handle.IsHandle())
	committedFrame, err := committed.ResultCall()
	require.NoError(t, err)
	payload, err := committedFrame.CallPB(ctx)
	require.NoError(t, err)

	// A later descendant call in the resolver context is not the committed tip.
	descendant := testResultCall("withToolResult", &LLM{}, frame)
	ctx = dagql.ContextWithCall(ctx, descendant)
	rt := testRuntime(t, ctx)
	rt.testTransition(func() { rt.commitLast(ctx, committed) })
	rt.testTransition(func() { rt.paused = true })
	next, err := dagql.NewObjectResultForCall(&LLM{}, srv, descendant)
	require.NoError(t, err)
	nextPayload, err := descendant.CallPB(ctx)
	require.NoError(t, err)
	rt.testTransition(func() { rt.commitLast(ctx, next) })

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.control, 3)
	require.Len(t, rec.records, 3, "agent publication does not re-emit call payloads")
	for i, record := range rec.control {
		projection, _, err := agentcontrol.Decode(record)
		require.NoError(t, err)
		require.Empty(t, projection.CaptureError)
		require.EqualValues(t, i+1, projection.Revision)
		want := payload.Digest
		if i == 2 {
			want = nextPayload.Digest
		}
		require.Equal(t, want, projection.Digest)
		if i > 0 {
			require.Equal(t, "PAUSED", projection.State)
		}
	}
}

func TestControlCaptureFailureReplacesPreviousAnchor(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(t, ctx)
	rt.testTransition(func() {})
	rt.testTransition(func() {
		rt.commitLast(ctx, dagql.ObjectResult[*LLM]{})
	})
	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.control, 2)
	first, _, err := agentcontrol.Decode(rec.control[0])
	require.NoError(t, err)
	last, _, err := agentcontrol.Decode(rec.control[1])
	require.NoError(t, err)
	require.NotEmpty(t, first.Digest)
	require.Empty(t, last.Digest)
	require.ErrorContains(t, errors.New(last.CaptureError), "no committed conversation")
	require.Greater(t, last.Revision, first.Revision)
	_, err = last.RestoreState()
	require.Error(t, err)
}

func TestNotifySkipsLevelCheckForInertRestore(t *testing.T) {
	registry := NewAgentRuntimes()
	ctx := testAgentContext(t, t.Context(), "worker", "worker")
	worker, _ := AgentFromContext(ctx)
	ctx = testAgentContext(t, ctx, "chief", "chief")
	chief, _ := AgentFromContext(ctx)
	watched := newAgentRuntime(registry, "worker", worker)
	subscriber := newAgentRuntime(registry, "chief", chief)
	watched.restored, subscriber.restored = true, true
	registry.entries["worker"], registry.entries["chief"] = watched, subscriber
	require.NoError(t, registry.Notify(ctx, worker, chief, []AgentState{AgentStateIdle}))
	watched.mu.Lock()
	require.Empty(t, watched.eventQueue, "restoration does not announce a historical completion")
	require.False(t, watched.eventDispatchRunning)
	watched.mu.Unlock()
	require.Empty(t, subscriber.mailbox, "restoration does not enqueue a historical completion")
	require.False(t, subscriber.started, "restoration never launches a model loop")
	watched.mu.Lock()
	// Hold event dispatch so assertions can inspect the deterministic queue.
	watched.eventDispatchRunning = true
	watched.transitionLocked(func() { watched.stepping = true })
	watched.transitionLocked(func() { watched.stepping = false })
	require.Empty(t, watched.eventQueue, "state alone is not a committed-work watermark")
	watched.transitionLocked(func() { watched.stepping = true })
	watched.transitionLocked(func() { watched.idleEventDue = true; watched.stepping = false })
	require.Len(t, watched.eventQueue, 1, "a new committed turn notifies the restored subscriber")
	require.Equal(t, "chief", watched.eventQueue[0].subscriberKey)
	watched.eventQueue = nil
	watched.mu.Unlock()
	require.NoError(t, registry.Notify(ctx, worker, chief, []AgentState{AgentStateFailed}))
	watched.mu.Lock()
	watched.transitionLocked(func() { watched.stepping = true })
	watched.transitionLocked(func() { watched.idleEventDue = true; watched.stepping = false })
	require.Empty(t, watched.eventQueue, "replacement filter suppresses nonmatching states")
	watched.transitionLocked(func() { watched.done = true; watched.loopErr = errors.New("failed") })
	require.Len(t, watched.eventQueue, 1)
	watched.eventQueue = nil
	watched.mu.Unlock()
	require.NoError(t, registry.Notify(ctx, worker, chief, nil))
	watched.mu.Lock()
	require.Empty(t, watched.subs["chief"].states)
	watched.mu.Unlock()

	// Once activated, or when never restored, the watched agent's current
	// state was reached in this session and the level check applies again.
	for _, tc := range []struct {
		name                string
		restored, activated bool
	}{
		{"activated restore", true, true},
		{"fresh entry", false, false},
	} {
		watched.mu.Lock()
		watched.restored, watched.activated = tc.restored, tc.activated
		watched.mu.Unlock()
		require.NoError(t, registry.Notify(ctx, worker, chief, []AgentState{AgentStateFailed}), tc.name)
		watched.mu.Lock()
		require.Len(t, watched.eventQueue, 1, tc.name)
		watched.eventQueue = nil
		watched.mu.Unlock()
	}
}

func TestCloseControlWaitsForProducersAndPreservesCause(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(t, ctx)
	registry := NewAgentRuntimes()
	registry.entries[rt.key] = rt
	rt.started = true
	loopCtx, cancel := context.WithCancelCause(context.Background())
	rt.cancel = cancel
	rt.testTransition(func() { rt.paused = true })
	closing, timeout := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer timeout()
	cause := errors.New("session transport closed")
	require.Error(t, registry.KillAll(closing, cause))
	require.ErrorIs(t, context.Cause(loopCtx), cause, "finalization retains teardown's original cancellation cause")
	require.False(t, rt.controlClosed, "failed stop cannot fix an archive cut or close its publisher")
	select {
	case <-rt.control.done:
		t.Fatal("control publisher closed before producer quiescence")
	default:
	}
	rt.testTransition(func() { rt.done = true }) // simulate loop's completed unwind
	expect, err := registry.CloseControl(t.Context())
	require.NoError(t, err)
	lastRevision := rt.controlRevision
	require.Error(t, rt.Resume(t.Context()))
	require.Error(t, rt.Pause())
	require.Error(t, rt.Interrupt())
	require.NoError(t, rt.Stop(t.Context(), true, nil, AgentStopExplicit))
	require.Equal(t, lastRevision, rt.controlRevision)
	again, err := registry.CloseControl(t.Context())
	require.NoError(t, err)
	require.Equal(t, expect, again, "successful fixed cut is idempotent")
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var idx agentcontrol.Index
	for _, record := range rec.control {
		_, err := idx.ApplyRecord(record)
		require.NoError(t, err)
	}
	require.NoError(t, idx.Verify(expect[""]))
	state, err := idx.Agents()[0].RestoreState()
	require.NoError(t, err)
	require.Equal(t, "PAUSED", state, "capture pre-teardown facts before cancellation rewrites them")
}

func TestDiscardRestoreKeepsRemovalWitness(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	ctx = testAgentContext(t, ctx, "restored", "restored")
	agent, _ := AgentFromContext(ctx)
	registry := NewAgentRuntimes()
	rt := testRuntime(t, ctx)
	rt.key, rt.restored, rt.ars = "restored", true, registry
	registry.entries[rt.key] = rt
	rt.testTransition(func() {})
	require.NoError(t, registry.DiscardRestore(ctx, agent))
	_, found, err := registry.Get(ctx, agent)
	require.NoError(t, err)
	require.False(t, found)
	expected, err := registry.CloseControl(context.Background())
	require.NoError(t, err)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var idx agentcontrol.Index
	for _, record := range rec.control {
		_, err := idx.ApplyRecord(record)
		require.NoError(t, err)
	}
	require.NoError(t, idx.Verify(expected[""]))
	require.True(t, idx.Agents()[0].Removed)
}
