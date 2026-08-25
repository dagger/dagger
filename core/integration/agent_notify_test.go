package core

// Integration tests for the event-driven agent messaging kernel
// (hack/designs/agent-messaging.md): lifecycle subscriptions delivering
// events as attributed mailbox messages, and the settled wait that cannot
// hang on a failed loop. The waits-for guard and reply resolution are pinned
// at the unit level (core/agent_messaging_test.go); the staff E2E
// (staff_test.go) covers the full cross-agent choreography.

import (
	"context"
	"fmt"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestNotifyDeliversLifecycleEvents covers §4.3 end to end: a subscriber
// hears a watched agent settle as an EVENT-origin message carrying the
// final reply, delivered through the ordinary mailbox — waking the idle
// subscriber like any other message — with no polling and no blocking wait
// anywhere.
func (AgentRuntimeSuite) TestNotifyDeliversLifecycleEvents(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	workerModel := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("do the thing").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))
	// The chief's whole conversation is the event: its arrival OPENS the
	// turn (the chief is idle until then), which is the wake-on-event
	// contract. The recording must hold the rendered wire text — header
	// plus the engine's event body — exactly as the model receives it.
	chiefModel := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt(agentIdleEventText("w", "done")).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "noted"},
		}))

	chief := spawnAgent(ctx, t, c, spawnOpts{model: chiefModel, name: "chief"})
	worker := spawnAgent(ctx, t, c, spawnOpts{model: workerModel, name: "w"})

	// Start the chief's loop: wake-on-event assumes a loop parked in
	// receive (the roster's everyday shape — a CLI conversation's loop is
	// started by its first submit). An unstarted subscriber would keep the
	// event queued instead of losing it, but this test wants the wake.
	_, err := chief.run(ctx, t, `start`)
	require.NoError(t, err)

	// Task first, then subscribe — the staff ordering. The subscription is
	// race-proof by construction: a still-running worker fires the IDLE
	// edge later; a worker that already finished trips the subscribe-time
	// level check immediately. Exactly one event either way.
	_, err = worker.sendID(ctx, t, "do the thing")
	require.NoError(t, err)
	_, err = worker.run(ctx, t, fmt.Sprintf(`notify(subscriber: %q)`, chief.agentID))
	require.NoError(t, err)

	// The event wakes the chief and its turn consumes it.
	require.Eventually(t, func() bool {
		_, lastReply := chief.snapshot(ctx, t)
		return lastReply == "noted"
	}, 90*time.Second, 500*time.Millisecond, "the worker's idle event never woke the chief")

	// The consumed event carries its structured origin: EVENT, naming the
	// watched agent — what a frontend needs to render it as a one-liner
	// rather than a user prompt.
	out := chief.mustRun(ctx, t, `snapshot { messages { role origin { kind agentName } } }`)
	var eventOrigin bool
	for _, msg := range out.Get("snapshot.messages").Array() {
		origin := msg.Get("origin")
		if origin.Exists() && origin.Get("kind").String() == "EVENT" {
			require.Equal(t, "w", origin.Get("agentName").String())
			eventOrigin = true
		}
	}
	require.True(t, eventOrigin, "the event message must carry an EVENT origin")
}

// TestWaitSettled covers §4.4: the settled wait returns on FAILED — where
// waitFor(IDLE) would hang forever, the dogfooded collect() pitfall — and
// Agent.error exposes why the loop failed.
func (AgentRuntimeSuite) TestWaitSettled(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// An empty recording fails its first step, so the send lands (delivery
	// is conclusive at the drain) and the loop then dies.
	h := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "flaky"})
	delivery, err := h.sendNoWait(ctx, t, "hi")
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)

	// waitSettled returns instead of hanging; state says how it settled,
	// and error says why.
	_, err = h.run(ctx, t, `waitSettled`)
	require.NoError(t, err)
	require.Equal(t, "FAILED", h.state(ctx, t))
	loopErr := h.mustRun(ctx, t, `error`).Get("error").String()
	require.Contains(t, loopErr, "no more messages")

	// An inert agent projects IDLE — already settled — so the wait returns
	// immediately rather than erroring or blocking.
	inert := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "inert"})
	_, err = inert.run(ctx, t, `waitSettled`)
	require.NoError(t, err)
	require.Equal(t, "IDLE", inert.state(ctx, t))
}
