package core

// These tests cover Agent! object-tool arguments (hack/designs/async-agents.md
// §3.1): a module function may declare an argument of the core Agent type. The
// MCP tool schema hides that argument, and object-tool dispatch explicitly
// passes the agent that owns the conversation: inside an agent loop, the agent
// running it. The module's GraphQL schema remains unchanged. This is the
// child->parent channel, letting a tool message (steer) the agent that called
// it.
//
// Style follows agent_runtime_test.go: keyless replay/ models constructed
// through the LLM API itself, real tools dispatched during replay, and no
// sleeps as synchronization — the self-send's STEERED delivery evidence is
// guaranteed by the loop's step-boundary drain, not by timing.

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const (
	pokePrompt = "go poke your parent"
	pokeNote   = "steered!"
	pokeReply  = "done"
)

// servePokerModule serves the agent-poker fixture module — poke(caller:
// Agent!, note: String!), which fire-and-forgets note to the MCP-supplied
// caller and confirms the send, whoami(caller: Agent!), and maybePoke with an
// optional caller — into the client's session and returns the Poker object's
// ID for llm.withTools.
func servePokerModule(ctx context.Context, t *testctx.T, c *dagger.Client) core.ID {
	t.Helper()
	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "agent-poker")
	require.NoError(t, core.NewQuery(c).ModuleSource(modDir).AsModule().Serve(ctx))
	res, err := testutil.QueryWithClient[struct {
		Poker struct {
			ID string
		}
	}](c, t, `{ poker { id } }`, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Poker.ID)
	return core.ID(res.Poker.ID)
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

	// A required caller and an optional one are both filled with the calling
	// agent inside its loop.
	for _, tool := range []string{"poke", "maybePoke"} {
		t.Run(tool, func(ctx context.Context, t *testctx.T) {
			model := cannedRecordingModel(ctx, t, c, core.NewQuery(c).LLM().
				WithPrompt(pokePrompt).
				WithResponse([]core.LLMContentBlockInput{
					{Kind: core.LLMContentBlockKindText, Text: "Poking the parent."},
					{Kind: core.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: tool,
						Arguments: core.JSON(fmt.Sprintf(`{"note":%q}`, pokeNote))},
				}).
				// Placeholder result: the real module tool runs during replay (tool
				// results are excluded from the replayer's history matching), so its
				// live confirmation flows through.
				WithToolResult("call_1", "", false).
				// The self-send joins the in-flight turn and drains at the step
				// boundary right after the tool result lands — the loop records it
				// as a prompt exactly here.
				WithPrompt(pokeNote).
				WithResponse([]core.LLMContentBlockInput{
					{Kind: core.LLMContentBlockKindText, Text: pokeReply},
				}))

			h := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "poked-" + tool, toolIDs: []core.ID{pokerID}})

			delivery, reply, err := h.sendAndWait(ctx, t, pokePrompt)
			require.NoError(t, err)
			require.Equal(t, "STARTED", delivery)
			require.Equal(t, pokeReply, reply)

			// The tool confirms it sent through the MCP-supplied handle, and the
			// note itself is on the record: influence ⇔ append.
			transcript, lastReply := h.snapshot(ctx, t)
			require.Contains(t, transcript, "sent")
			require.NotContains(t, transcript, "no caller")
			require.Contains(t, transcript, pokeNote)
			require.Equal(t, pokeReply, lastReply)
		})
	}
}

// TestAgentArgHiddenFromToolSchema locks in that an Agent argument never
// reaches the model, and that a method requiring one is offered exactly when
// the conversation has an owning agent to supply, whoever reads the toolset.
// An unowned conversation hides poke entirely rather than offering it and
// failing, and offers maybePoke without its optional caller, which it receives
// as null. An agent's snapshot, read here from outside any loop, offers both.
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
	require.NotContains(t, doc, "## poke")
	require.Contains(t, doc, "## maybePoke")
	require.Contains(t, doc, `"note"`)
	require.NotContains(t, doc, `"caller"`)

	owner := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "owner", toolIDs: []core.ID{pokerID}})
	doc = owner.mustRun(ctx, t, "snapshot { tools }").Get("snapshot.tools").String()
	require.Contains(t, doc, "## poke\n")
	require.Contains(t, doc, "## whoami\n")
	require.Contains(t, doc, "## maybePoke")
	require.NotContains(t, doc, `"caller"`)
}

// TestAgentArgFromOwnerInSyncLoop covers a synchronous loop over an agent's
// conversation: no agent loop runs, but the conversation is owned, so a tool
// requiring an Agent is offered and receives the owner.
func (AgentRuntimeSuite) TestAgentArgFromOwnerInSyncLoop(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	pokerID := servePokerModule(ctx, t, c)
	owner := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "owner", toolIDs: []core.ID{pokerID}})
	snapshot := core.Ref[*core.LLM](core.NewQuery(c), core.ID(owner.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))

	model := cannedRecordingModel(ctx, t, c, core.NewQuery(c).LLM().
		WithPrompt("who am I").
		WithResponse([]core.LLMContentBlockInput{
			{Kind: core.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "whoami", Arguments: core.JSON(`{}`)},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]core.LLMContentBlockInput{
			{Kind: core.LLMContentBlockKindText, Text: pokeReply},
		}))
	transcript, err := snapshot.WithModel(model).WithPrompt("who am I").Loop().Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "caller: owner")
	require.NotContains(t, transcript, "is not available")
}

// TestAgentArgRequiresAgentLoop covers the no-agent behavior without weakening
// the module schema. A direct GraphQL call still has to supply the declared
// Agent! argument; only the MCP object-tool adapter fills it. A synchronous
// loop over an unowned conversation has no agent to pass, so a tool requiring
// one is not offered at all, and one taking an optional caller receives null.
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

	// syncLoop runs one recorded synchronous turn calling tool, and returns
	// the transcript.
	syncLoop := func(ctx context.Context, t *testctx.T, tool string, errored bool) string {
		t.Helper()
		model := cannedRecordingModel(ctx, t, c, core.NewQuery(c).LLM().
			WithPrompt(pokePrompt).
			WithResponse([]core.LLMContentBlockInput{
				{Kind: core.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: tool,
					Arguments: core.JSON(fmt.Sprintf(`{"note":%q}`, pokeNote))},
			}).
			WithToolResult("call_1", "", errored).
			WithResponse([]core.LLMContentBlockInput{
				{Kind: core.LLMContentBlockKindText, Text: pokeReply},
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
		return res.LLM.WithTools.WithPrompt.Loop.Transcript
	}

	t.Run("synchronous loop", func(ctx context.Context, t *testctx.T) {
		// The tool is hidden under a sync loop, so a call to it is a call to
		// an unavailable tool: the error becomes the tool's errored result and
		// the recorded turn still closes.
		transcript := syncLoop(ctx, t, "poke", true)
		require.Contains(t, transcript, `tool "poke" is not available`)
		require.NotContains(t, transcript, "function requires the calling agent")
	})

	t.Run("synchronous loop with an optional caller", func(ctx context.Context, t *testctx.T) {
		// The tool is offered without its caller argument, which it receives
		// as null.
		transcript := syncLoop(ctx, t, "maybePoke", false)
		require.Contains(t, transcript, "no caller for "+pokeNote)
		require.NotContains(t, transcript, "is not available")
	})
}
