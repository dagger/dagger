package core

// These tests cover Agent! tool-argument injection (hack/designs/async-agents.md
// §3.1): a module function may declare an argument of the core Agent type;
// that argument is hidden from the tool schema the model sees, and when the
// function is dispatched as a tool from a RUNNING AGENT LOOP the engine
// auto-injects the calling agent's handle — the child->parent channel, letting
// a tool message (steer) the agent that called it. Invoked outside a loop, the
// function fails with a clear error instead of silently receiving nothing.
//
// Style follows agent_runtime_test.go: keyless replay/ models constructed
// through the LLM API itself, real tools dispatched during replay, and no
// sleeps as synchronization — the self-send's STEERED delivery evidence is
// guaranteed by the loop's step-boundary drain, not by timing.

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const (
	pokePrompt = "go poke your parent"
	pokeNote   = "steered!"
	pokeReply  = "done"
)

// servePokerModule serves the agent-poker fixture module — one function,
// poke(caller: Agent!, note: String!): String!, which fire-and-forgets note
// to the injected caller and returns the send's delivery evidence — into the
// client's session and returns the Poker object's ID for llm.withTools.
func servePokerModule(ctx context.Context, t *testctx.T, c *dagger.Client) dagger.ID {
	t.Helper()
	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "agent-poker")
	require.NoError(t, c.ModuleSource(modDir).AsModule().Serve(ctx))
	res, err := testutil.QueryWithClient[struct {
		Poker struct {
			ID string
		}
	}](c, t, `{ poker { id } }`, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Poker.ID)
	return dagger.ID(res.Poker.ID)
}

// TestAgentArgInjection covers the happy path end to end: a recorded tool
// call to poke makes the module message the CALLING agent through the
// injected handle, fire-and-forget. The self-send lands while the turn is in
// flight — STEERED delivery evidence, visible in the tool's live result —
// and its text drains onto the record at the next step boundary (which is
// exactly where the recording places it), so the turn closes with the
// recording's final reply and the note in its history.
func (AgentRuntimeSuite) TestAgentArgInjection(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	pokerID := servePokerModule(ctx, t, c)

	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt(pokePrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Poking the parent."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "poke",
				Arguments: dagger.JSON(fmt.Sprintf(`{"note":%q}`, pokeNote))},
		}).
		// Placeholder result: the real module tool runs during replay (tool
		// results are excluded from the replayer's history matching), so its
		// live result — the delivery evidence — flows through.
		WithToolResult("call_1", "", false).
		// The self-send joins the in-flight turn and drains at the step
		// boundary right after the tool result lands — the loop records it
		// as a prompt exactly here.
		WithPrompt(pokeNote).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: pokeReply},
		}))

	h := &agentHandle{c: c, model: model, name: "poked", toolIDs: []dagger.ID{pokerID}}

	delivery, reply, err := h.sendAndWait(ctx, t, pokePrompt)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
	require.Equal(t, pokeReply, reply)

	// The tool observed its self-send absorbed into the in-flight turn (the
	// STEERED evidence in its live result), and the note itself is on the
	// record: influence ⇔ append.
	transcript, lastReply := h.snapshot(ctx, t)
	require.Contains(t, transcript, "delivery: STEERED")
	require.Contains(t, transcript, pokeNote)
	require.Equal(t, pokeReply, lastReply)
}

// TestAgentArgHiddenFromToolSchema locks in that an Agent! argument never
// reaches the model: the rendered toolset documents poke with its note
// parameter, but no caller parameter — the engine fills it, exactly like the
// auto-injected Workspace!/LLM! arguments.
func (AgentRuntimeSuite) TestAgentArgHiddenFromToolSchema(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	pokerID := servePokerModule(ctx, t, c)

	res, err := testutil.QueryWithClient[struct {
		LLM struct {
			WithTools struct {
				Tools string
			}
		} `json:"llm"`
	}](c, t, `query($model: String!, $poker: ID!) {
		llm(model: $model) { withTools(object: $poker) { tools } }
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"model": emptyReplayModel,
		"poker": pokerID,
	}})
	require.NoError(t, err)

	doc := res.LLM.WithTools.Tools
	require.Contains(t, doc, "## poke")
	require.Contains(t, doc, `"note"`)
	require.NotContains(t, doc, `"caller"`)
}

// TestAgentArgRequiresAgentLoop covers the no-agent failure mode: a function
// with an Agent! argument invoked outside a running agent loop — directly,
// or as a tool of a synchronous LLM.loop — fails with a clear error rather
// than silently passing null.
func (AgentRuntimeSuite) TestAgentArgRequiresAgentLoop(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	pokerID := servePokerModule(ctx, t, c)

	t.Run("direct call", func(ctx context.Context, t *testctx.T) {
		_, err := testutil.QueryWithClient[struct {
			Poker struct {
				Poke string
			}
		}](c, t, `{ poker { poke(note: "hello") } }`, nil)
		require.ErrorContains(t, err,
			"function requires the calling agent; invoke it from an agent loop (LLM.asAgent)")
	})

	t.Run("synchronous loop", func(ctx context.Context, t *testctx.T) {
		// The tool call fails (no agent in context under a sync loop); the
		// error becomes the tool's errored result and the recorded turn
		// still closes, so the message is asserted from the transcript.
		model := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt(pokePrompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "poke",
					Arguments: dagger.JSON(fmt.Sprintf(`{"note":%q}`, pokeNote))},
			}).
			WithToolResult("call_1", "", true).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: pokeReply},
			}))

		res, err := testutil.QueryWithClient[struct {
			LLM struct {
				WithTools struct {
					WithPrompt struct {
						Loop struct {
							Transcript string
						}
					}
				}
			} `json:"llm"`
		}](c, t, `query($model: String!, $poker: ID!, $prompt: String!) {
			llm(model: $model) { withTools(object: $poker) { withPrompt(prompt: $prompt) { loop { transcript } } } }
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"model":  model,
			"poker":  pokerID,
			"prompt": pokePrompt,
		}})
		require.NoError(t, err)
		require.Contains(t, res.LLM.WithTools.WithPrompt.Loop.Transcript,
			"function requires the calling agent")
	})
}
