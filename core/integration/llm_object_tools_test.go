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

// TestToolReturningWorkspaceRebinds locks in that a tool returning a Workspace
// *replaces* the LLM's current workspace — the sibling of the Changeset overlay
// convention (routeObjectMethodResult -> applyStateReturn). The swapper module's
// `swap` tool returns the current workspace with a marker file added; after the
// model turn that calls it, the LLM's workspace must be that returned one.
//
// The replay recording scripts one full turn: prompt -> assistant tool call ->
// tool result -> a final assistant reply with no tool calls, so `loop`
// terminates cleanly. The replayer only diffs a message's TEXT blocks, so the
// tool-result placeholder needs no matching summary text. This is deterministic
// and needs no live provider.
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
