package core

// Tests for the object-tools scheme (LLM.withTools). See
// hack/designs/workspace-agents.md.
//
// These exercise the live schema through the shell DSL, so they run on a
// from-source engine without needing the SDK regenerated for withTools:
//   dagger --x-release <ver> call engine-dev test \
//     --run 'TestLLM/TestObjectToolset' --pkg ./core/integration --test-verbose

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestObjectToolset locks in that the LLM's tools come from the objects it's
// bound to via withTools — one tool per eligible method — and not from the raw
// workspace schema. A bare llm (nothing bound) has no acting tools; the retired
// Dang scheme's dang_eval/inspect are gone from the default toolset.
func (LLMSuite) TestObjectToolset(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-managed")

	t.Run("bare llm exposes no acting tools", func(ctx context.Context, t *testctx.T) {
		// The default llm auto-binds the current workspace for schema derivation,
		// but binds no object as tools, so it acts through nothing until withTools.
		tools, err := base.With(daggerShell("llm | tools")).Stdout(ctx)
		require.NoError(t, err)

		// The retired Dang scheme's tools are no longer the default interface.
		require.NotContains(t, tools, "## dang_eval\n")
		require.NotContains(t, tools, "## inspect\n")

		// The workspace's served functions are not exposed as tools on their own —
		// a model reaches a method only once its object is bound via withTools.
		require.NotContains(t, tools, "## greet\n")
		require.NotContains(t, tools, "## greeter\n")
	})

	t.Run("withTools exposes a bound object's methods", func(ctx context.Context, t *testctx.T) {
		// Bind the greeter module's object; each of its eligible methods becomes a
		// tool named after the method.
		tools, err := base.With(daggerShell("llm | with-tools $(greeter) | tools")).Stdout(ctx)
		require.NoError(t, err)

		// greet is a method on the bound Greeter object -> a tool.
		require.Contains(t, tools, "## greet\n")

		// greeter is the Query-root constructor, not a method of the bound object,
		// so it is not a tool. Nor is the retired Dang harness present.
		require.NotContains(t, tools, "## greeter\n")
		require.NotContains(t, tools, "## dang_eval\n")
		require.NotContains(t, tools, "## inspect\n")
	})

	t.Run("except hides methods from the toolset", func(ctx context.Context, t *testctx.T) {
		// The except list drops named methods (e.g. an entrypoint you don't want
		// the model calling on itself).
		tools, err := base.With(daggerShell(`llm | with-tools $(greeter) --except greet | tools`)).Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, tools, "## greet\n")
	})
}

// TestParallelChangesetToolsMergeResults locks in that Changeset-returning tools
// from one model response run as a batch and all of their changes are retained.
func (LLMSuite) TestParallelChangesetToolsMergeResults(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-tool-return")

	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("make both changes").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "addFirst"},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "addSecond"},
		}).
		WithToolResult("call_1", "", false).
		WithToolResult("call_2", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))

	out, err := base.With(daggerShell(fmt.Sprintf(
		`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "make both changes" | loop | workspace | directory "/" | entries`,
		model,
	))).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "FIRST.txt")
	require.Contains(t, out, "SECOND.txt")
}

// TestParallelChangesetToolsPreserveConflicts locks in that a batch whose
// changesets *cannot* merge cleanly is not thrown away. Two tools rewrite the
// same lines of the same file; the octopus merge refuses that, so the batch
// falls back to replaying the changesets as patches with conflict markers.
// Both sides' work survives, and the agent gets a tree it can repair.
func (LLMSuite) TestParallelChangesetToolsPreserveConflicts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-tool-return")

	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("make both changes").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "clashFirst"},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "clashSecond"},
		}).
		WithToolResult("call_1", "", false).
		WithToolResult("call_2", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))

	out, err := base.With(daggerShell(fmt.Sprintf(
		`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "make both changes" | loop | workspace | file shared.txt | contents`,
		model,
	))).Stdout(ctx)
	require.NoError(t, err)

	// Neither side was discarded: both edits are present in the merged file,
	// bracketed by conflict markers. Before the conflict-preserving fallback
	// this file still read "line1: placeholder".
	require.Contains(t, out, "RED")
	require.Contains(t, out, "BLUE")
	require.Contains(t, out, "<<<<<<<")
	require.Contains(t, out, ">>>>>>>")
	require.NotContains(t, out, "placeholder")
}

// TestToolReturningWorkspaceRebinds locks in that a tool returning a Workspace
// *replaces* the LLM's current workspace — the sibling of the Changeset overlay
// convention (routeObjectMethodResult -> applyStateReturn).
func (LLMSuite) TestToolReturningWorkspaceRebinds(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-tool-return")

	// The assistant calls the swap tool (its Workspace! arg is auto-injected, so
	// no arguments are passed); swap returns currentWorkspace + SWAPPED.txt.
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("swap the workspace").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "swap"},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))

	t.Run("the returned workspace becomes the LLM's workspace", func(ctx context.Context, t *testctx.T) {
		// If the returned Workspace were merely synced/described (the pre-existing
		// fall-through), the LLM's workspace would stay the base one and this file
		// lookup would error — so the assertion doubles as the discriminator.
		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-tools $(swapper) | with-prompt "swap the workspace" | loop | workspace | file SWAPPED.txt | contents`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "swapped by tool", strings.TrimSpace(out))
	})

	t.Run("the base workspace does not already contain the marker", func(ctx context.Context, t *testctx.T) {
		// Control: the marker only exists because the tool produced it, not because
		// the fixture shipped it.
		_, err := base.With(daggerShell(
			`current-workspace | file SWAPPED.txt | contents`,
		)).Stdout(ctx)
		require.Error(t, err)
	})
}

// TestToolReturningLLMContinues locks in the continuation ring of the state-return
// convention: a tool that returns an LLM replaces the conversation, and the loop
// resumes from the returned one (routeObjectMethodResult -> applyStateReturn ->
// adoptLLM). The tool's LLM! argument is auto-injected with the conversation up
// to and including its own call, so `install`/`reload`-style self-extension is a
// plain transform.
func (LLMSuite) TestToolReturningLLMContinues(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-tool-return")

	t.Run("the loop resumes from the returned conversation", func(ctx context.Context, t *testctx.T) {
		model := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt("continue").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "continueWithMarker"},
			}).
			WithToolResult("call_1", "", false).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		// continueWithMarker returns llm.withWorkspace(<workspace + marker>). The
		// marker is only reachable if the loop adopted the RETURNED LLM: without
		// the continuation arm the returned LLM would merely be synced and
		// described, leaving the original workspace bound, and this would error.
		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "continue" | loop | workspace | file CONTINUED.txt | contents`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "swapped by continuation", strings.TrimSpace(out))
	})

	t.Run("the conversation survives the swap", func(ctx context.Context, t *testctx.T) {
		model := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt("continue").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "continueWithMarker"},
			}).
			WithToolResult("call_1", "", false).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		// The turn's tool result is appended to the returned LLM, and the loop
		// carries on from there — the prompt, the tool call and the final reply
		// are all in one transcript.
		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "continue" | loop | transcript`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "continue")
		require.Contains(t, out, "Continuing from the returned conversation.")
		require.Contains(t, out, "done")
	})

	t.Run("a conversation that does not continue the current one is rejected", func(ctx context.Context, t *testctx.T) {
		model := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt("hijack").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "hijack"},
			}).
			WithToolResult("call_1", "", true).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		// hijack wipes the history it was handed. That is a failed tool call, not a
		// dead agent: the loop runs to completion on the ORIGINAL conversation.
		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "hijack" | loop | transcript`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "does not continue the current conversation")
		require.Contains(t, out, "done")
	})

	t.Run("at most one continuation per turn", func(ctx context.Context, t *testctx.T) {
		model := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt("continue twice").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "continueWithMarker"},
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "continueWithMarker"},
			}).
			WithToolResult("call_1", "", false).
			WithToolResult("call_2", "", true).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		// LLMs do not merge the way Changesets do, so the second swap in a batch is
		// refused rather than silently discarding the first.
		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "continue twice" | loop | transcript`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "only one is allowed")
		require.Contains(t, out, "done")
	})

	t.Run("the base workspace does not already contain the marker", func(ctx context.Context, t *testctx.T) {
		_, err := base.With(daggerShell(
			`current-workspace | file CONTINUED.txt | contents`,
		)).Stdout(ctx)
		require.Error(t, err)
	})
}

