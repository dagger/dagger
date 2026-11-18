package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func editTestRuntime(t *testing.T) (*AgentRuntime, func(string, bool) dagql.ObjectResult[*LLM]) {
	t.Helper()
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*LLM](srv))
	conversation := func(name string, pending bool) dagql.ObjectResult[*LLM] {
		role := LLMMessageRoleAssistant
		if pending {
			role = LLMMessageRoleUser
		}
		llm, err := dagql.NewObjectResultForCall(&LLM{Messages: []*LLMMessage{{Role: role, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: name}}}}}, srv, &dagql.ResultCall{
			Kind: dagql.ResultCallKindSynthetic, SyntheticOp: name,
			Type: dagql.NewResultCallType((&LLM{}).Type()),
		})
		require.NoError(t, err)
		return llm
	}
	rt := &AgentRuntime{name: "edited", messages: map[string]*agentMessageRecord{}, stateChanged: make(chan struct{}), wake: make(chan struct{}, 1), last: conversation("seed", false)}
	t.Cleanup(func() { rt.AbortConversationEdit("edit"); rt.AbortConversationEdit("other") })
	return rt, conversation
}

func TestConversationEditPreservesTurn(t *testing.T) {
	rt, conversation := editTestRuntime(t)
	rt.started, rt.turnOpen = true, true
	ref, err := rt.enqueue("original prompt", nil, "")
	require.NoError(t, err)
	rec := rt.messages[ref]
	rt.mailbox = nil
	rec.consumed = true
	rt.consumed = []*agentMessageRecord{rec}
	_, err = rt.BeginConversationEdit(t.Context(), "edit")
	require.NoError(t, err)
	_, err = rt.BeginConversationEdit(t.Context(), "other")
	require.ErrorContains(t, err, "another")
	require.ErrorContains(t, rt.Reseed(t.Context(), conversation("wrong", true)), "boundary")
	rt.AbortConversationEdit("other")
	require.NotNil(t, rt.edit)
	require.NoError(t, rt.CommitConversationEdit(t.Context(), "edit", conversation("compacted summary", true)))
	require.True(t, rt.turnOpen)
	require.False(t, rt.held)
	require.Same(t, rec, rt.consumed[0])
	require.False(t, rec.resolved)
	require.Equal(t, AgentStateRunning, rt.State())
}

func TestConversationEditReleaseBeforeSettlement(t *testing.T) {
	rt, conversation := editTestRuntime(t)
	rt.started, rt.turnOpen = true, true
	ref, err := rt.enqueue("original prompt", nil, "")
	require.NoError(t, err)
	rec := rt.messages[ref]
	rt.mailbox = nil
	rec.consumed = true
	rt.consumed = []*agentMessageRecord{rec}
	_, err = rt.BeginConversationEdit(t.Context(), "edit")
	require.NoError(t, err)

	// Schedule precisely the loop's vulnerable window: it observes the
	// barrier and leaves its step loop, but does not yet lock for settlement.
	observed, settle := make(chan struct{}), make(chan struct{})
	continued := make(chan bool, 1)
	go func() {
		rt.mu.Lock()
		barrier := rt.edit != nil
		rt.mu.Unlock()
		if !barrier {
			t.Error("expected loop to observe edit barrier")
		}
		close(observed)
		<-settle
		continued <- rt.continueTurnOrSettle(t.Context())
	}()
	<-observed
	require.NoError(t, rt.CommitConversationEdit(t.Context(), "edit", conversation("pending compacted summary", true)))
	close(settle)
	require.True(t, <-continued, "released edit's pending input must continue the existing turn")
	require.False(t, rec.resolved)
	require.True(t, rt.turnOpen)

	rt.mu.Lock()
	rt.transitionLocked(func() { rt.commitLast(t.Context(), conversation("final continuation reply", false)) })
	rt.mu.Unlock()
	require.False(t, rt.continueTurnOrSettle(t.Context()))
	require.True(t, rec.resolved)
	require.Equal(t, "final continuation reply", rec.reply)
	require.NoError(t, rec.err)
}

func TestConversationEditPreservesExistingPending(t *testing.T) {
	for _, state := range []string{"failed", "paused", "never-started"} {
		t.Run(state, func(t *testing.T) {
			rt, conversation := editTestRuntime(t)
			rt.last = conversation("existing pending prompt", true)
			switch state {
			case "failed":
				rt.started, rt.done, rt.loopErr = true, true, errors.New("provider failed")
			case "paused":
				rt.paused = true
			}
			before := rt.State()
			_, err := rt.BeginConversationEdit(t.Context(), "edit")
			require.NoError(t, err)
			require.NoError(t, rt.CommitConversationEdit(t.Context(), "edit", conversation("same prompt after route edit", true)))
			require.False(t, rt.held, "existing retryable input must retain its resume semantics")
			require.Equal(t, before, rt.State())
		})
	}
}

func TestConversationEditKeepsIdlePausedAndFailed(t *testing.T) {
	for _, state := range []string{"never-started", "idle", "paused", "failed"} {
		t.Run(state, func(t *testing.T) {
			rt, conversation := editTestRuntime(t)
			switch state {
			case "idle":
				rt.started = true
			case "paused":
				rt.started, rt.paused, rt.turnOpen = true, true, true
			case "failed":
				rt.started, rt.done, rt.loopErr = true, true, errors.New("provider failed")
			}
			before := rt.State()
			_, err := rt.BeginConversationEdit(t.Context(), "edit")
			require.NoError(t, err)
			require.NoError(t, rt.CommitConversationEdit(t.Context(), "edit", conversation("summary", true)))
			require.Equal(t, before, rt.State())
			require.Equal(t, state != "paused", rt.held)
			if state == "never-started" {
				require.False(t, rt.started)
			}
			if state == "failed" {
				require.EqualError(t, rt.loopErr, "provider failed")
			}
		})
	}
}

func TestConversationEditWaitCancelExpiryAndConflict(t *testing.T) {
	t.Run("waits for drain and cancellation releases", func(t *testing.T) {
		rt, _ := editTestRuntime(t)
		rt.draining = true
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { _, err := rt.BeginConversationEdit(ctx, "edit"); done <- err }()
		require.Eventually(t, func() bool { rt.mu.Lock(); defer rt.mu.Unlock(); return rt.edit != nil }, time.Second, time.Millisecond)
		select {
		case err := <-done:
			t.Fatalf("drain was not awaited: %v", err)
		default:
		}
		cancel()
		require.ErrorIs(t, <-done, context.Canceled)
		rt.mu.Lock()
		require.Nil(t, rt.edit)
		rt.draining = false
		rt.mu.Unlock()
	})
	t.Run("lease starts after boundary wait", func(t *testing.T) {
		rt, _ := editTestRuntime(t)
		rt.stepping = true
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		const lease = 20 * time.Millisecond
		done := make(chan error, 1)
		go func() { _, err := rt.beginConversationEdit(ctx, "edit", lease); done <- err }()
		require.Eventually(t, func() bool { rt.mu.Lock(); defer rt.mu.Unlock(); return rt.edit != nil }, time.Second, time.Millisecond)
		time.Sleep(3 * lease)
		rt.mu.Lock()
		edit := rt.edit
		var timer *time.Timer
		if edit != nil {
			timer = edit.timer
		}
		rt.transitionLocked(func() { rt.stepping = false })
		rt.mu.Unlock()
		require.NotNil(t, edit, "a live wait for a long step must not expire")
		require.Nil(t, timer)
		require.NoError(t, <-done)
		require.Eventually(t, func() bool { rt.mu.Lock(); defer rt.mu.Unlock(); return rt.edit == nil }, time.Second, time.Millisecond)
	})
	t.Run("lease expiry", func(t *testing.T) {
		rt, conversation := editTestRuntime(t)
		_, err := rt.beginConversationEdit(t.Context(), "edit", 10*time.Millisecond)
		require.NoError(t, err)
		require.Eventually(t, func() bool { rt.mu.Lock(); defer rt.mu.Unlock(); return rt.edit == nil }, time.Second, time.Millisecond)
		require.ErrorContains(t, rt.CommitConversationEdit(t.Context(), "edit", conversation("too late", true)), "not active")
		require.Equal(t, AgentStateIdle, rt.State())
	})
	t.Run("stop releases lease", func(t *testing.T) {
		rt, _ := editTestRuntime(t)
		_, err := rt.BeginConversationEdit(t.Context(), "edit")
		require.NoError(t, err)
		require.NoError(t, rt.Stop(t.Context(), true, nil, AgentStopSession))
		require.Nil(t, rt.edit)
		_, err = rt.BeginConversationEdit(t.Context(), "other")
		require.Error(t, err)
	})
	t.Run("canceled commit preserves snapshot", func(t *testing.T) {
		rt, conversation := editTestRuntime(t)
		original := rt.Snapshot()
		_, err := rt.BeginConversationEdit(t.Context(), "edit")
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		require.ErrorIs(t, rt.CommitConversationEdit(ctx, "edit", conversation("not committed", true)), context.Canceled)
		rt.AbortConversationEdit("edit")
		require.Same(t, original.Self(), rt.Snapshot().Self())
	})
	t.Run("changed snapshot conflicts", func(t *testing.T) {
		rt, conversation := editTestRuntime(t)
		_, err := rt.BeginConversationEdit(t.Context(), "edit")
		require.NoError(t, err)
		rt.mu.Lock()
		rt.transitionLocked(func() { rt.commitLast(t.Context(), conversation("other commit", false)) })
		rt.mu.Unlock()
		require.ErrorContains(t, rt.CommitConversationEdit(t.Context(), "edit", conversation("stale edit", true)), "changed")
		rt.AbortConversationEdit("edit")
		rt.AbortConversationEdit("edit")
		require.Nil(t, rt.edit)
	})
}
