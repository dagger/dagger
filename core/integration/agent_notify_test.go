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
func agentIdleEventText(name, finalReply string) string {
	return fmt.Sprintf("[event from agent %q]\n\nAgent %q is now idle.\n\nIts final reply:\n\n%s",
		name, name, finalReply)
}

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

// TestWait covers §4.4: the settled wait returns on FAILED — where
// waitFor(IDLE) would hang forever, the dogfooded collect() pitfall — and
// Agent.error exposes why the loop failed.
func (AgentRuntimeSuite) TestWait(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// An empty recording fails its first step, so the send lands (delivery
	// is conclusive at the drain) and the loop then dies.
	h := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "flaky"})
	delivery, err := h.sendNoWait(ctx, t, "hi")
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)

	// wait returns instead of hanging; state says how it settled,
	// and error says why.
	_, err = h.run(ctx, t, `wait`)
	require.NoError(t, err)
	require.Equal(t, "FAILED", h.state(ctx, t))
	loopErr := h.mustRun(ctx, t, `error`).Get("error").String()
	require.Contains(t, loopErr, "no more messages")

	// An inert agent projects IDLE — already settled — so the wait returns
	// immediately rather than erroring or blocking.
	inert := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "inert"})
	_, err = inert.run(ctx, t, `wait`)
	require.NoError(t, err)
	require.Equal(t, "IDLE", inert.state(ctx, t))
}

// TestResumeRetryEmitsNoStaleIdle pins the resume-retry flow against the
// stale idle event observed in dogfooding: resuming a FAILED worker
// relaunches its loop with the mailbox still empty (the staff sendTo is
// resume-first, send-second), and the relaunch window used to project a
// transient IDLE — firing an idle event that carried the PREVIOUS turn's
// final reply, which a supervising chief reads as a fresh completion. The
// fix is twofold: the relaunch restores the suspended-turn fact when the
// snapshot holds a pending (failed) step, and an IDLE edge with no newly
// committed work no longer fans out at all.
func (AgentRuntimeSuite) TestResumeRetryEmitsNoStaleIdle(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	// One recorded exchange: the worker's second turn exhausts the
	// recording and FAILS, and every resume-retry fails the same way.
	workerModel := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("do the thing").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))
	// The chief's recording holds exactly ONE exchange: the real
	// completion. A stale idle event would open a second chief turn this
	// recording cannot serve, failing the chief — loudly visible below.
	chiefModel := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt(agentIdleEventText("w", "done")).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "noted"},
		}))

	chief := spawnAgent(ctx, t, c, spawnOpts{model: chiefModel, name: "chief"})
	worker := spawnAgent(ctx, t, c, spawnOpts{model: workerModel, name: "w"})

	_, err := chief.run(ctx, t, `start`)
	require.NoError(t, err)

	// Task first, then subscribe — the staff ordering (see
	// TestNotifyDeliversLifecycleEvents): a still-running worker fires the
	// IDLE edge later; one already finished trips the subscribe-time level
	// check, carrying the same "done" reply either way. IDLE only: the
	// FAILED transitions below are expected and not under test — any idle
	// event past the first is the bug.
	_, err = worker.sendID(ctx, t, "do the thing")
	require.NoError(t, err)
	_, err = worker.run(ctx, t, fmt.Sprintf(`notify(subscriber: %q, on: [IDLE])`, chief.agentID))
	require.NoError(t, err)

	// Turn 1 completes; its idle event reaches the chief.
	require.Eventually(t, func() bool {
		_, lastReply := chief.snapshot(ctx, t)
		return lastReply == "noted"
	}, 90*time.Second, 500*time.Millisecond, "the real idle event never reached the chief")

	// Turn 2 consumes its message, then the step fails (recording
	// exhausted). The chief hears nothing: FAILED is not subscribed.
	delivery, err := worker.sendNoWait(ctx, t, "again")
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
	_, err = worker.run(ctx, t, `wait`)
	require.NoError(t, err)
	require.Equal(t, "FAILED", worker.state(ctx, t))

	// Resume retries the failed step and fails identically — twice, since
	// once would prove nothing if the first relaunch consumed some
	// one-shot guard. Neither relaunch may fire an idle event.
	for range 2 {
		_, err = worker.run(ctx, t, `resume`)
		require.NoError(t, err)
		_, err = worker.run(ctx, t, `wait`)
		require.NoError(t, err)
		require.Equal(t, "FAILED", worker.state(ctx, t))
	}

	// Give a stale event time to land before counting: delivery and the
	// chief's consuming turn are both replay-instant, so a short dwell
	// bounds the negative assertion honestly.
	time.Sleep(2 * time.Second)
	require.Equal(t, "IDLE", chief.state(ctx, t),
		"a stale idle event would have opened (and failed) a second chief turn")
	out := chief.mustRun(ctx, t, `snapshot { messages { origin { kind } } }`)
	var events int
	for _, msg := range out.Get("snapshot.messages").Array() {
		if msg.Get("origin.kind").String() == "EVENT" {
			events++
		}
	}
	require.Equal(t, 1, events,
		"the resume relaunch must not re-announce the previous turn's reply")
}
