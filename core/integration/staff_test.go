package core

// End-to-end test for modules/staff — the async orchestration module built on
// the Agent runtime (hack/designs/async-agents.md) and its event-driven
// messaging (hack/designs/agent-messaging.md): a chief agent spawns a named
// worker, the worker's askChief question is DELIVERED without blocking and
// arrives on the chief's record with attribution, the worker's completions
// arrive as lifecycle events carrying its final reply, and the chief answers
// with sendTo(replyTo:) — pairing the reply with the question in the worker's
// history — while nobody ever blocks on anybody's turn.
//
// Style follows agent_runtime_test.go: keyless replay/ models constructed
// through the LLM API, real tools dispatched during replay, and no sleeps as
// synchronization. The cross-agent races are de-raced structurally: whenever
// the chief's recording expects a worker message or event at a step boundary,
// the chief is dwelling in a slow tool call long enough for the worker's
// (instant, replay-driven) turn to land it. Attribution headers and event
// texts are deterministic by design — refs are per-runtime ordinals, never
// minted IDs — which is what makes recording them at all possible.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	fscopy "github.com/dagger/dagger/internal/fsutil/copy"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type StaffSuite struct{}

func TestStaff(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(StaffSuite{})
}

// serveStaffModule serves ./modules/staff — a repo-root module, deliberately
// not a testdata fixture and not registered in dagger.toml — into the
// client's session, returning the Staff object's ID for llm.withTools and
// the worker system prompt spawn composes into a worker with the given
// staff name (recordings that replay that worker's conversation must match
// its text exactly — the name interpolation is part of workerPromptFor's
// public contract).
func serveStaffModule(ctx context.Context, t *testctx.T, c *dagger.Client, workerName string) (dagger.ID, string) {
	t.Helper()
	modDir := t.TempDir()
	srcDir, err := filepath.Abs(filepath.Join("..", "..", "modules", "staff"))
	require.NoError(t, err)
	require.NoError(t, fscopy.Copy(ctx, srcDir, "/", modDir, "/"))
	require.NoError(t, c.ModuleSource(modDir).AsModule().Serve(ctx))
	res, err := testutil.QueryWithClient[struct {
		Staff struct {
			ID              string
			WorkerPromptFor string
		}
	}](c, t, fmt.Sprintf(`{ staff { id workerPromptFor(name: %q) } }`, workerName), nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Staff.ID)
	require.NotEmpty(t, res.Staff.WorkerPromptFor)
	// The composed prompt must actually carry the worker's own staff name
	// (async-agents.md item 10) — without this, spawn and the recording
	// would still agree byte for byte on an uninterpolated prompt and the
	// replay would prove nothing about the name.
	require.Contains(t, res.Staff.WorkerPromptFor,
		fmt.Sprintf("Your staff name is %q", workerName))
	return dagger.ID(res.Staff.ID), res.Staff.WorkerPromptFor
}

// The deterministic wire forms of hack/designs/agent-messaging.md §4.1,
// mirrored from core (LLMMessageOrigin.AttributionHeader, eventTextLocked).
// Recordings must contain the RENDERED text — the replayer compares what the
// model would actually receive.

func agentMsgText(ref, from, body string) string {
	return fmt.Sprintf("[message %s from agent %q]\n\n%s", ref, from, body)
}

func agentReplyText(from, replyTo, body string) string {
	return fmt.Sprintf("[reply from agent %q to your message %s]\n\n%s", from, replyTo, body)
}

func agentIdleEventText(name, finalReply string) string {
	return fmt.Sprintf("[event from agent %q]\n\nAgent %q is now idle.\n\nIts final reply:\n\n%s",
		name, name, finalReply)
}

// TestAskAndReply covers the whole event-driven orchestration loop: spawn
// starts a named worker in the background and subscribes the chief to its
// lifecycle; the worker's askChief question is delivered non-blocking and
// steers into the chief's open turn WITH attribution; the worker ends its
// turn to wait, and its idle event (carrying its final reply) reaches the
// chief; the chief answers mid-turn with sendTo(replyTo:), which wakes the
// worker with the paired reply; and the worker's final completion arrives as
// a second event. No blocking edge exists anywhere in the exchange — the
// shape the old ask/askChief deadlocks (agent-messaging.md §2) made
// impossible.
func (StaffSuite) TestAskAndReply(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Bound the whole test: if an event or reply never lands, the replay
	// diverges or an agent idles forever, and a bounded context turns that
	// into a fast, located failure instead of a go-test-timeout abort.
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	// The worker recording's system prompt must be the exact text spawn
	// composes for a worker named "w1" — workerPromptFor interpolates the
	// staff name, so the worker knows what to call itself.
	staffID, workerPrompt := serveStaffModule(ctx, t, c, "w1")

	// spawn auto-injects `source: Workspace!`, preferring the calling LLM's
	// bound workspace. Bind an explicit empty one to the chief: the fallback
	// — the session's ambient currentWorkspace — is whatever the harness
	// session inherited. That could be nothing (spawn errors) or a real
	// workspace like the dagger repo itself, whose registered modules
	// include live @agent middlewares that compose would fold into the
	// worker, diverging its replay. An empty workspace has no middlewares,
	// so compose is identity over the base the recordings expect.
	wsID, err := c.Directory().AsWorkspace().ID(ctx)
	require.NoError(t, err)

	const (
		chiefPrompt1  = "hire w1 for the yak-shaving and start the slow work"
		workerTask    = "shave the yak; ask the chief if anything is unclear"
		question      = "what color should the bikeshed be?"
		workerStandby = "Asked the chief; standing by."
		answer        = "the bikeshed must be cornflower blue"
		chiefReply1   = "w1 asked about the bikeshed and is standing by."
		workerFinal   = "Yak shaved. The chief said: " + answer
		chiefPrompt2  = "answer w1, then wrap up the slow work"
		chiefReply2   = "w1 delivered its report."
	)

	// The worker's canned conversation, exactly as the wire will see it:
	// the spawned task arrives attributed to the chief (worker inbox #1),
	// askChief is dispatched and returns IMMEDIATELY (its delivery-receipt
	// result is excluded from history matching, like every tool result),
	// the worker ends its turn to wait — that is the waiting verb now —
	// and the chief's answer arrives as a paired reply (worker inbox #2,
	// replying to chief inbox #2), waking it for its final turn.
	workerModel := cannedReplayModel(ctx, t, c, c.LLM().
		WithSystemPrompt(workerPrompt).
		WithPrompt(agentMsgText("#1", "chief", workerTask)).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Unclear indeed - asking the chief."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "askChief",
				Arguments: dagger.JSON(fmt.Sprintf(`{"question":%q}`, question))},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: workerStandby},
		}).
		WithPrompt(agentReplyText("chief", "#2", answer)).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: workerFinal},
		}))

	// The slow tool the chief dwells in right after spawning: while it
	// sleeps, the freshly spawned worker runs its whole first turn — the
	// question steers into the chief's open turn (chief inbox #2), and the
	// worker's idle event follows it (#3), both draining at the boundary
	// after the tool result, in enqueue order.
	vol := c.CacheVolume("staff-askreply-" + identity.NewID())
	ctrID, err := slowToolContainer(c, vol, 6).ID(ctx)
	require.NoError(t, err)

	// The chief's canned conversation, de-raced by a rule the second turn
	// shares: NEVER leave a step boundary between starting cross-agent
	// traffic and the dwell that absorbs its effects. Each turn is ONE tool
	// batch — destructive calls (spawn/sendTo/withExec rebinds) run
	// sequentially before the read-only stdout, so the turn's only step
	// boundary comes after the 6s dwell, by which time the worker's
	// (instant, replay-driven) activity has landed in the mailbox. Turn 1:
	// spawn w1 + dwell, then the worker's attributed question and first
	// idle event, then close — WITHOUT answering, so no cross-agent edge
	// ever blocks. Turn 2: sendTo answers mid-turn (replyTo pairs it with
	// the question), withExec chains a second sleep onto the bound
	// container (a rebind, so it is not cached), stdout dwells in it, and
	// the worker's final idle event lands inside the open turn.
	chiefModel := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt(chiefPrompt1).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Hiring w1 and starting the slow work."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "spawn",
				Arguments: dagger.JSON(fmt.Sprintf(`{"name":"w1","task":%q,"model":%q}`,
					workerTask, workerModel))},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "stdout"},
		}).
		WithToolResult("call_1", "", false).
		WithToolResult("call_2", "", false).
		WithPrompt(agentMsgText("#2", "w1", question)).
		WithPrompt(agentIdleEventText("w1", workerStandby)).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: chiefReply1},
		}).
		WithPrompt(chiefPrompt2).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Answering w1, then dwelling while it wraps up."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_3", ToolName: "sendTo",
				Arguments: dagger.JSON(fmt.Sprintf(`{"name":"w1","message":%q,"replyTo":"#2"}`,
					answer))},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_4", ToolName: "withExec",
				Arguments: dagger.JSON(`{"args":["sh","-c","sleep 6 && echo TOOL2-DONE"]}`)},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_5", ToolName: "stdout"},
		}).
		WithToolResult("call_3", "", false).
		WithToolResult("call_4", "", false).
		WithToolResult("call_5", "", false).
		WithPrompt(agentIdleEventText("w1", workerFinal)).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: chiefReply2},
		}))

	h := spawnAgent(ctx, t, c, spawnOpts{model: chiefModel, name: "chief",
		toolIDs: []dagger.ID{staffID, ctrID}, wsID: wsID})

	// Turn 1: spawn, dwell, and absorb the worker's question + idle event.
	// The replay matching is the proof: a question that never steered, a
	// missing attribution header, or a missing event would all diverge the
	// chief's replay and fail the turn.
	delivery, reply, err := h.sendAndWait(ctx, t, chiefPrompt1)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
	require.Equal(t, chiefReply1, reply)

	// Influence ⇔ append, now with provenance: the question is on the
	// chief's record, and its structured origin says who asked.
	transcript, lastReply := h.snapshot(ctx, t)
	require.Contains(t, transcript, question)
	require.Equal(t, chiefReply1, lastReply)
	origins := h.mustRun(ctx, t,
		`snapshot { messages { role origin { kind agentName ref } } }`)
	var questionOrigin, eventOrigin bool
	for _, msg := range origins.Get("snapshot.messages").Array() {
		origin := msg.Get("origin")
		if !origin.Exists() || origin.Type.String() == "Null" {
			continue
		}
		switch origin.Get("kind").String() {
		case "AGENT":
			require.Equal(t, "w1", origin.Get("agentName").String())
			require.Equal(t, "#2", origin.Get("ref").String())
			questionOrigin = true
		case "EVENT":
			require.Equal(t, "w1", origin.Get("agentName").String())
			eventOrigin = true
		}
	}
	require.True(t, questionOrigin, "the worker's question must carry an AGENT origin")
	require.True(t, eventOrigin, "the worker's completion must carry an EVENT origin")

	// Turn 2: the chief answers mid-turn with sendTo(replyTo: "#2") — no
	// turn-end conflation, no blocking — and the worker's final completion
	// event lands inside the chief's dwell. The worker's replay proves the
	// pairing: its recording expects the answer as a "[reply ... to your
	// message #2]" message, so a reply that arrived unpaired (or never
	// arrived) would diverge the worker and its final event would never
	// carry workerFinal.
	delivery, reply, err = h.sendAndWait(ctx, t, chiefPrompt2)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
	require.Equal(t, chiefReply2, reply)

	transcript, lastReply = h.snapshot(ctx, t)
	require.Contains(t, transcript, workerFinal)
	require.Equal(t, chiefReply2, lastReply)
}
