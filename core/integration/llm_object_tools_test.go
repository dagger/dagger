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
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call"
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

// TestChangesetToolKeepsEmptyDirectories locks in that a Changeset-returning
// tool's empty directories survive the engine's patch normalization
// (core.normalizeChangesetToPatch). Git patches carry file content only, so
// without the directory reconciliation the normalized changeset — which
// replaces the original on the live workspace binding — would silently drop
// the empty directory while keeping the file beside it.
func (LLMSuite) TestChangesetToolKeepsEmptyDirectories(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-tool-return")

	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("scaffold the project").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "addScaffold"},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))

	t.Run("the file beside the empty directory lands", func(ctx context.Context, t *testctx.T) {
		// Control: the file edit rode the patch path, so normalization ran
		// rather than falling back to the raw changeset.
		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "scaffold the project" | loop | workspace | file scaffold/README.md | contents`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "scaffolded", strings.TrimSpace(out))
	})

	t.Run("the empty directory survives normalization", func(ctx context.Context, t *testctx.T) {
		// Resolving the directory errors if the patch round trip dropped it.
		_, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "scaffold the project" | loop | workspace | directory scaffold/empty-dir | entries`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err,
			"an empty directory created by a tool's changeset must survive patch normalization")
	})

	t.Run("normalization actually ran", func(ctx context.Context, t *testctx.T) {
		// The empty directory would also survive if normalization silently
		// fell back to the raw changeset, so the subtest above cannot tell
		// reconciliation from a skipped normalization. The recorded overlay
		// discriminates: a normalized overlay is withPatch plus the
		// withNewDirectory that restored the empty directory, while the raw
		// changeset's chain has the tool's operations and no withPatch.
		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "scaffold the project" | loop | portable-id`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)

		gid := new(call.ID)
		require.NoError(t, gid.Decode(strings.TrimSpace(out)))
		fields := map[string]bool{}
		collectIDFieldNames(gid, fields)
		require.True(t, fields["withPatch"],
			"the recorded overlay must be patch-normalized, not the raw changeset")
		require.True(t, fields["withNewDirectory"],
			"the reconciliation must record the empty directory's restoration")
	})
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

// collectIDFieldNames records every field name reachable in an ID — the
// receiver spine plus every ID nested in argument literals.
func collectIDFieldNames(id *call.ID, into map[string]bool) {
	for cur := id; cur != nil; cur = cur.Receiver() {
		into[cur.Field()] = true
		for _, arg := range cur.Args() {
			collectLiteralFieldNames(arg.Value(), into)
		}
	}
}

func collectLiteralFieldNames(lit call.Literal, into map[string]bool) {
	switch v := lit.(type) {
	case *call.LiteralID:
		collectIDFieldNames(v.Value(), into)
	case *call.LiteralList:
		for _, item := range v.Values() {
			collectLiteralFieldNames(item, into)
		}
	case *call.LiteralObject:
		for _, field := range v.Args() {
			if field == nil {
				continue
			}
			collectLiteralFieldNames(field.Value(), into)
		}
	}
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

	t.Run("a conversation that replaces the current one is adopted", func(ctx context.Context, t *testctx.T) {
		model := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt("start fresh").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "startFresh"},
			}).
			WithToolResult("call_1", "", false).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		// The adopted conversation replays a *second* script: after adoption its
		// history is not the one the outer script recorded, but the engine's
		// degraded continuation notice (toolResultSelectors' plain-message arm,
		// carrying summarizeContinuation's summary as the tool result). The
		// fixture's startFresh points the fresh conversation at this model, read
		// from the workspace file written below. If summarizeContinuation's
		// wording — or the swapper's tool count — changes, this string has to
		// change with it; the replay provider compares message text exactly.
		continued := strings.Join([]string{
			"[continued via tool startFresh]",
			"Continuing from the returned conversation.",
			"Toolset unchanged (14 tools).",
			"Conversation history replaced: 2 messages -> 0 messages.",
		}, "\n")
		continuationModel := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt(continued).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		// startFresh wipes the history it was handed. There is no lineage gate, so
		// it is adopted like any other continuation (self-compaction and
		// summarize-and-restart have exactly this shape) — and the model is TOLD,
		// which is what makes the swap safe. The turn's tool result has no matching
		// tool call in the adopted history, so it is carried as a plain message
		// rather than a protocol-invalid tool result.
		out, err := base.
			WithNewFile("continuation-model.txt", continuationModel).
			With(daggerShell(fmt.Sprintf(
				`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "start fresh" | loop | transcript`,
				model,
			))).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Continuing from the returned conversation.")
		require.Contains(t, out, "Conversation history replaced:")
		require.Contains(t, out, "[continued via tool startFresh]")
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

// TestAddressableToolArgs covers address lifting of object-typed tool args end
// to end (hack/designs/sandboxes.md §4): a module function with a required
// Container! arg still becomes a tool — the arg renders as an address string,
// and a model-supplied image ref is lifted into a real container via
// Query.address at dispatch — while a required arg of any other object type
// (here Directory!) still disqualifies its function, since Container is the
// only type to have passed the capability review for model-typed address
// strings (liftableTypes in core/llm_object_tools.go).
func (LLMSuite) TestAddressableToolArgs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-addressable-args")

	t.Run("a required Container arg renders as an address", func(ctx context.Context, t *testctx.T) {
		tools, err := base.With(daggerShell("llm | with-tools $(runner) | tools")).Stdout(ctx)
		require.NoError(t, err)

		// exec IS a tool despite its required Container! arg...
		require.Contains(t, tools, "## exec\n")
		// ...and its sandbox parameter is described as an address — with the
		// type's syntax hint from liftableTypes — not as a bare ID.
		require.Contains(t, tools, "(Container address:")
		require.Contains(t, tools, "or a Container ID from a prior tool result")

		// lsDir's required Directory! arg is not liftable (host-path
		// fallback), so lsDir is not exposed as a tool.
		require.NotContains(t, tools, "## lsDir\n")
	})

	t.Run("an image ref lifts into a real container", func(ctx context.Context, t *testctx.T) {
		model := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt("what OS is the sandbox running?").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "exec",
					Arguments: dagger.JSON(fmt.Sprintf(`{"cmd":["cat","/etc/os-release"],"sandbox":%q}`, alpineImage))},
			}).
			// Placeholder result: the real tool runs during replay (tool
			// results are excluded from the replayer's history matching), so
			// the live stdout flows through.
			WithToolResult("call_1", "", false).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-tools $(runner) | with-prompt "what OS is the sandbox running?" | loop | transcript`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)
		// The tool result is the stdout of the command run inside the lifted
		// container — proof the address resolved to the real image. "Alpine
		// Linux" appears only in /etc/os-release; the tool call's argument
		// says "alpine:<version>", so a failed lift can't false-positive.
		require.Contains(t, out, "Alpine Linux")
	})

	t.Run("an encoded Container ID round-trips", func(ctx context.Context, t *testctx.T) {
		const marker = "address-lift round-trip"
		ctrID, err := c.Container().From(alpineImage).
			WithNewFile("/marker.txt", marker).ID(ctx)
		require.NoError(t, err)
		args, err := json.Marshal(map[string]any{
			"cmd":     []string{"cat", "/marker.txt"},
			"sandbox": string(ctrID),
		})
		require.NoError(t, err)

		model := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt("read the marker").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "exec",
					Arguments: dagger.JSON(args)},
			}).
			WithToolResult("call_1", "", false).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-tools $(runner) | with-prompt "read the marker" | loop | transcript`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)
		// The marker is plaintext only inside the container's filesystem —
		// in the tool call's arguments it is buried in the encoded
		// (protobuf+base64) ID — so seeing it in the transcript proves the
		// ID decoded directly into the same container, no address lookup.
		require.Contains(t, out, marker)
	})
}
