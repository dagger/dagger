package core

import (
	"errors"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// A spawn's seed may end in a prompt (llm.withPrompt("x").spawn()). Completing
// the composition must leave that input for the loop to step — spawn's and
// resume's contract — rather than holding it the way a reseed does.
func TestCompleteCompositionLeavesSeedInputUnheld(t *testing.T) {
	_, ctx := stateRecorderCtx(t)
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*LLM](srv))
	prompt := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "x"}}}
	frame := testResultCall("withPrompt", &LLM{}, testResultCall("llm", &LLM{}, nil))
	seed, err := dagql.NewObjectResultForCall(&LLM{Messages: []*LLMMessage{prompt}, OwnerAgentHandle: "test"}, srv, frame)
	require.NoError(t, err)
	require.True(t, seed.Self().HasPending())

	rt := testRuntime(t, ctx)
	rt.composing = true
	rt.held = true // a stale hold must not survive the composed seed's commit
	require.NoError(t, rt.CompleteComposition(ctx, rt.self, seed))
	require.False(t, rt.held, "the seed's pending input is the loop's to step on send or resume")
	require.False(t, rt.composing)
	require.Same(t, seed.Self(), rt.Snapshot().Self())
	require.Equal(t, AgentStateIdle, rt.State(), "a never-started agent is IDLE until a send or resume starts it")

	// Once a loop is live, the unheld pending seed projects as work about to
	// step, not as a completed turn.
	rt.testTransition(func() { rt.started = true })
	require.Equal(t, AgentStateRunning, rt.State())
}

// A spawn whose composition fails leaves a STOPPED tombstone reserving its
// handle. Unlike an ordinary STOPPED agent, nothing may relaunch it: its
// committed conversation is the provisional seed, without the expertise.
func TestAbortCompositionRefusesRelaunch(t *testing.T) {
	base, released := leasedAgentContext(t)
	registry := NewAgentRuntimes()
	ctx := testAgentContext(t, base, "doomed", "doomed")
	agent, ok := AgentFromContext(ctx)
	require.True(t, ok)
	rt, err := registry.Prepare(ctx, agent, AgentStateIdle, "", false, "")
	require.NoError(t, err)
	_, err = registry.Require(ctx, agent)
	require.ErrorContains(t, err, "still composing")
	// Reads of the conversation refuse too: the provisional seed lacks the
	// expertise being composed.
	_, err = rt.Seed()
	require.ErrorContains(t, err, "still composing")
	_, err = rt.ComposedSnapshot()
	require.ErrorContains(t, err, "still composing")

	cause := errors.New("expertise exploded")
	rt.AbortComposition(ctx, cause)
	require.Equal(t, AgentStateStopped, rt.State())
	_, err = rt.ComposedSnapshot()
	require.NoError(t, err, "a tombstone's conversation stays readable")

	// The tombstone stays addressable for reads and stop.
	again, err := registry.Require(ctx, agent)
	require.NoError(t, err)
	require.Same(t, rt, again)
	require.Equal(t, "doomed", rt.Name())
	_ = rt.Snapshot()
	require.NoError(t, rt.Stop(base, false, nil, AgentStopExplicit), "stopping a tombstone stays idempotent")
	require.NoError(t, rt.Stop(base, true, nil, AgentStopExplicit))
	require.Equal(t, AgentStateStopped, rt.State())

	// Every verb that would run or replace the provisional conversation is
	// refused with the composition's cause.
	assertAborted := func(err error) {
		t.Helper()
		require.ErrorIs(t, err, cause)
		require.ErrorContains(t, err, `agent "doomed" failed its initial composition and cannot be run`)
	}
	assertAborted(rt.Resume(ctx))
	assertAborted(rt.start(ctx))
	_, err = rt.enqueue("hello", nil, "")
	assertAborted(err)
	_, err = registry.Send(ctx, agent, "hello", "")
	assertAborted(err)
	assertAborted(rt.Reseed(ctx, rt.Snapshot()))
	assertAborted(registry.Reseed(ctx, agent, rt.Snapshot()))
	_, err = rt.enqueue("event", &LLMMessageOrigin{Kind: LLMMessageOriginEvent}, "")
	require.ErrorIs(t, err, errAgentEventDropped, "events to the tombstone are dropped, as for any stopped subscriber")

	rt.mu.Lock()
	require.False(t, rt.started, "no loop ever ran the provisional seed")
	require.Empty(t, rt.mailbox)
	rt.mu.Unlock()
	require.Equal(t, AgentStateStopped, rt.State())

	// Session teardown still releases the tombstone.
	require.NoError(t, registry.KillAll(t.Context(), nil))
	_, err = registry.CloseControl(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, 1, released.Load())
}
