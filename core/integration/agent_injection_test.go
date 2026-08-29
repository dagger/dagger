package core

// These tests cover Agent! object-tool arguments (hack/designs/async-agents.md
// §3.1): a module function may declare an argument of the core Agent type. The
// MCP tool schema hides that argument, and object-tool dispatch explicitly
// passes the calling agent's handle when running inside an agent loop. The
// module's GraphQL schema remains unchanged. This is the child->parent channel,
// letting a tool message (steer) the agent that called it.
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
// to the MCP-supplied caller and confirms the send — into the
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
// MCP-supplied handle, fire-and-forget. The self-send lands while the turn is
// in flight and its text drains onto the record at the next step boundary
// (which is exactly where the recording places it), so the turn closes with
// the recording's final reply and the note in its history.
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
		// live confirmation flows through.
		WithToolResult("call_1", "", false).
		// The self-send joins the in-flight turn and drains at the step
		// boundary right after the tool result lands — the loop records it
		// as a prompt exactly here.
		WithPrompt(pokeNote).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: pokeReply},
		}))

	h := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "poked", toolIDs: []dagger.ID{pokerID}})

	delivery, reply, err := h.sendAndWait(ctx, t, pokePrompt)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
	require.Equal(t, pokeReply, reply)

	// The tool confirms it sent through the MCP-supplied handle, and the note
	// itself is on the record: influence ⇔ append.
	transcript, lastReply := h.snapshot(ctx, t)
	require.Contains(t, transcript, "sent")
	require.Contains(t, transcript, pokeNote)
	require.Equal(t, pokeReply, lastReply)
}

// TestAgentArgHiddenFromToolSchema locks in that an Agent! argument never
// reaches the model: the rendered toolset documents poke with its note
// parameter, but no caller parameter — MCP supplies it during object-tool
// dispatch alongside the Workspace and LLM arguments it owns.
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

// TestAgentArgRequiresAgentLoop covers the no-agent behavior without weakening
// the module schema. A direct GraphQL call still has to supply the declared
// Agent! argument; only the MCP object-tool adapter fills it. A synchronous
// LLM tool call has no agent to pass and returns a clear tool error.
func (AgentRuntimeSuite) TestAgentArgRequiresAgentLoop(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	pokerID := servePokerModule(ctx, t, c)

	t.Run("direct call", func(ctx context.Context, t *testctx.T) {
		_, err := testutil.QueryWithClient[struct {
			Poker struct {
				Poke string
			}
		}](c, t, `{ poker { poke(note: "hello") } }`, nil)
		require.ErrorContains(t, err, `is required, but it was not provided`)
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
