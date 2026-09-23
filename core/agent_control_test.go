package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/stretchr/testify/require"
)

func TestControlCaptureFailureReplacesPreviousAnchor(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(t, ctx)
	rt.testTransition(func() {})
	rt.testTransition(func() {
		rt.controlCapture = &agentCapture{}
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

func TestControlCaptureOutsideRuntimeLock(t *testing.T) {
	rec, ctx := stateRecorderCtx(t)
	rt := testRuntime(t, ctx)
	capture := &agentCapture{}
	entered, release := make(chan struct{}), make(chan struct{})
	go capture.once.Do(func() { close(entered); <-release; capture.digest = "xxh3:new" })
	<-entered
	rt.mu.Lock()
	rt.controlCapture = capture
	rt.transitionLocked(func() {})
	rt.mu.Unlock()
	// Recipe derivation is stalled. A newer coherent lifecycle revision can
	// still be assigned and queued without taking the derivation's lock.
	changed := make(chan struct{})
	go func() {
		rt.mu.Lock()
		rt.transitionLocked(func() { rt.paused = true })
		rt.mu.Unlock()
		close(changed)
	}()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("capture blocked the runtime mutex")
	}
	close(release)
	rt.flushControl()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var idx agentcontrol.Index
	for _, record := range rec.control {
		_, err := idx.ApplyRecord(record)
		require.NoError(t, err)
	}
	latest := idx.Agents()[0]
	require.Equal(t, "PAUSED", latest.State)
	require.Equal(t, int64(2), latest.Revision)
	require.Equal(t, "xxh3:new", latest.Digest)
}

func TestRestoreNotifySuppressesHistoryButKeepsFutureEdges(t *testing.T) {
	registry := NewAgentRuntimes()
	ctx := testAgentContext(t, t.Context(), "worker", "worker")
	worker, _ := AgentFromContext(ctx)
	ctx = testAgentContext(t, ctx, "chief", "chief")
	chief, _ := AgentFromContext(ctx)
	watched := newAgentRuntime(registry, "worker", worker)
	subscriber := newAgentRuntime(registry, "chief", chief)
	watched.restored, subscriber.restored = true, true
	registry.entries["worker"], registry.entries["chief"] = watched, subscriber
	require.NoError(t, registry.RestoreNotify(ctx, worker, chief, []AgentState{AgentStateIdle}))
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
	require.NoError(t, registry.RestoreNotify(ctx, worker, chief, []AgentState{AgentStateFailed}))
	watched.mu.Lock()
	watched.transitionLocked(func() { watched.stepping = true })
	watched.transitionLocked(func() { watched.idleEventDue = true; watched.stepping = false })
	require.Empty(t, watched.eventQueue, "replacement filter suppresses nonmatching states")
	watched.transitionLocked(func() { watched.done = true; watched.loopErr = errors.New("failed") })
	require.Len(t, watched.eventQueue, 1)
	watched.eventQueue = nil
	watched.mu.Unlock()
	require.NoError(t, registry.RestoreNotify(ctx, worker, chief, nil))
	watched.mu.Lock()
	require.Empty(t, watched.subs["chief"].states)
	watched.mu.Unlock()
	subscriber.activated = true
	require.Error(t, registry.RestoreNotify(ctx, worker, chief, nil))
	subscriber.activated, subscriber.restored = false, false
	require.Error(t, registry.RestoreNotify(ctx, worker, chief, nil), "fresh dormant entries are not restored provenance")
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
		t.Fatal("capture publisher closed before producer quiescence")
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
