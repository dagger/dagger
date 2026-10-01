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
	telemetry "github.com/dagger/otel-go"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/internal/buildkit/identity"
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

// TestDirectoryWorkspaceHandleBoundTool keeps module tools callable when
// withTools receives a runtime handle, as currentNode and same-type tool returns
// do. A value-backed workspace deliberately has only the core served schema, so
// the handle must be loaded and traced back to its recipe's module schema rather
// than pinning the core schema as the tool's definition.
func (LLMSuite) TestDirectoryWorkspaceHandleBoundTool(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := c.Directory().
		WithNewFile("dagger.toml", "[modules.editor]\nsource = \"modules/editor\"\n").
		WithNewFile("modules/editor/dagger.json", `{"name":"editor","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("modules/editor/main.dang", `
type Editor {
  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode)
  }

  todoWrite(item: String!): Editor! {
    print(item)
    self
  }
}
`)
	ws := source.AsWorkspace()
	model := cannedRecordingModel(ctx, t, c, c.LLM().
		WithPrompt("track it").
		WithResponse([]dagger.LLMContentBlockInput{{
			Kind:      dagger.LLMContentBlockKindToolCall,
			CallID:    "call_1",
			ToolName:  "todoWrite",
			Arguments: dagger.JSON(`{"item":"tracked"}`),
		}}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{{
			Kind: dagger.LLMContentBlockKindText,
			Text: "done",
		}}))
	base := c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws)
	composed, err := composeArtifactAgents(ctx, c, ws, nil, base)
	require.NoError(t, err)
	transcript, err := composed.
		WithPrompt("track it").
		Loop().
		Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "done")
}

// TestRestoredModuleTool loads a lazy module receiver that has never been
// evaluated. A warm constructor cache would hide a dispatch through the core
// schema, which is what broke tool calls after resuming an agent from a trace.
func (LLMSuite) TestRestoredModuleTool(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	source := c.Directory().
		WithNewFile("dagger.toml", "[modules.editor]\nsource = \"modules/editor\"\n").
		WithNewFile("modules/editor/dagger.json", `{"name":"editor","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("modules/editor/main.dang", `
type Editor {
  marker: String!

  new(marker: String! = "warm") {
    self.marker = marker
    self
  }

  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode)
  }

  readMarker: String! {
    "loaded " + marker
  }
}
`)
	// Recover the real constructor's provenance from a portable composition,
	// then give it a new argument so this receiver cannot already be cached.
	composed, err := composeArtifactAgents(ctx, c, source.AsWorkspace(), nil)
	require.NoError(t, err)
	portable, err := sink.captureLLMRecipe(ctx, t, c, composed)
	require.NoError(t, err)
	id := new(call.ID)
	require.NoError(t, id.Decode(string(portable)))
	var receiver *call.ID
	for cur := id; cur != nil; cur = cur.Receiver() {
		if cur.Field() != "withTools" {
			continue
		}
		for _, arg := range cur.Args() {
			if arg.Name() == "object" {
				receiver = arg.Value().(*call.LiteralID).Value()
			}
		}
		if receiver != nil {
			break
		}
	}
	require.NotNil(t, receiver)
	require.Equal(t, "editor", receiver.Field())
	marker := identity.NewID()
	receiver = receiver.With(call.WithArgs(call.NewArgument("marker", call.NewLiteralString(marker), false)))
	objectID, err := receiver.Encode()
	require.NoError(t, err)

	model := cannedRecordingModel(ctx, t, c, c.LLM().
		WithPrompt("before restore").
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "remembered"}}).
		WithPrompt("read the marker").
		WithResponse([]dagger.LLMContentBlockInput{{
			Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "readMarker",
		}}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "done"}}))
	var res struct {
		LLM struct {
			WithTools struct {
				ID    string
				Tools string
			}
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query: `query($model: String!, $object: ID!) {
			llm(model: $model) { withTools(object: $object) { id tools } }
		}`,
		Variables: map[string]any{"model": model, "object": objectID},
	}, &dagger.Response{Data: &res}))
	require.Contains(t, res.LLM.WithTools.Tools, "## readMarker")
	seed := dagger.Ref[*dagger.LLM](c, dagger.ID(res.LLM.WithTools.ID)).
		WithPrompt("before restore").
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "remembered"}})
	snapshot, err := sink.captureLLMRecipe(ctx, t, c, seed)
	require.NoError(t, err)

	// No modules are served into the restoring client's schema. Restore and
	// tool listing can succeed without loading the receiver; the next turn must
	// actually reconstruct it and invoke its module-defined method.
	target := connect(ctx, t)
	restored, err := rehydrateAgent(ctx, target, string(snapshot), identity.NewID(), "restored", "IDLE", "")
	require.NoError(t, err)
	_, reply, err := restored.sendAndWait(ctx, t, "read the marker")
	require.NoError(t, err)
	require.Equal(t, "done", reply)
	transcript, _ := restored.snapshot(ctx, t)
	require.Contains(t, transcript, "remembered")
	require.Contains(t, transcript, "loaded "+marker)
	require.NotContains(t, transcript, "load bound object")
}

// TestBatchedChangesetToolsKeepAllResults locks in that Changeset-returning
// tools from one model response all have their changes retained.
func (LLMSuite) TestBatchedChangesetToolsKeepAllResults(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-tool-return")

	model := cannedRecordingModel(ctx, t, c, c.LLM().
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

// TestWorkspaceToolSummaryPayloads checks the actual tool result and telemetry,
// including changes with few paths but potentially large or binary patches.
func (LLMSuite) TestWorkspaceToolSummaryPayloads(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name, setup, edit string
		generatePatch     bool
		showPatch         bool
	}{
		{name: "small text", setup: "true", generatePatch: true, showPatch: true},
		{name: "binary", setup: "printf '\\000binary payload' > payload.dat", edit: `.withoutFile("payload.dat")`},
		{name: "long line", setup: "head -c 327680 /dev/zero | tr '\\0' x > payload.dat", edit: `.withoutFile("payload.dat")`, generatePatch: true},
		{name: "rename", setup: "echo original > payload.dat", edit: `.withFile("renamed.dat", ws.file("payload.dat")).withoutFile("payload.dat")`},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			sink := newAgentTraceSink(t)
			c := connect(ctx, t, sink.clientOpts()...)
			base := workspaceFixture(t, c, "workspace-tool-return").
				WithNewFile(".dagger/modules/swapper/main.dang", fmt.Sprintf(`
type Swapper {
  swap(ws: Workspace!): Workspace! {
    ws%s.withNewFile("EDIT.txt", "ordinary edit\n")
  }
}
`, tc.edit)).WithExec([]string{"sh", "-ec", tc.setup})
			model := cannedRecordingModel(ctx, t, c, c.LLM().
				WithPrompt("swap the workspace").
				WithResponse([]dagger.LLMContentBlockInput{{
					Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "swap",
				}}).
				WithToolResult("call_1", "", false).
				WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "done"}}))
			out, err := base.With(daggerShell(fmt.Sprintf(
				`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "swap the workspace" | loop | transcript`, model,
			))).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "done")
			require.Contains(t, out, "EDIT.txt")
			require.NotContains(t, out, "GIT binary patch")
			require.NotContains(t, out, strings.Repeat("x", 1024))
			require.NotContains(t, out, "bytes truncated; read Changeset.asPatch")
			require.NotContains(t, out, "WARNING:")
			require.Less(t, len(out), 16*1024)
			if tc.showPatch {
				require.Contains(t, out, "diff --git")
				require.Contains(t, out, "+ordinary edit")
			} else {
				require.NotContains(t, out, "diff --git")
				require.Contains(t, out, "payload.dat")
			}

			// Closing flushes the nested CLI's forwarded spans and logs.
			require.NoError(t, c.Close())
			traces, logs := sink.capture()
			require.NotEmpty(t, traces)
			require.NotEmpty(t, logs)
			seenTool, seenPatch := false, false
			patchLogs := map[string]*strings.Builder{}
			for _, request := range traces {
				for _, resource := range request.ResourceSpans {
					for _, scope := range resource.ScopeSpans {
						for _, span := range scope.Spans {
							seenTool = seenTool || span.Name == "Swapper.swap"
							seenPatch = seenPatch || span.Name == "Changeset.asPatch"
							if span.Name == "git diff" {
								patchLogs[fmt.Sprintf("%x", span.SpanId)] = new(strings.Builder)
							}
							require.NotEqual(t, "Changeset.diffStats", span.Name)
							require.False(t, strings.HasPrefix(span.Name, "DiffStat."), "%s", span.Name)
						}
					}
				}
			}
			require.True(t, seenTool, "capture must include the workspace-returning tool")
			require.Equal(t, tc.generatePatch, seenPatch)
			// The nested CLI and parent SDK can forward the same record. Count
			// each emission once, retaining distinct writes with identical text.
			type logKey struct {
				span, body string
				timestamp  uint64
			}
			seenLogs := map[logKey]bool{}
			for _, request := range logs {
				for _, resource := range request.ResourceLogs {
					for _, scope := range resource.ScopeLogs {
						for _, record := range scope.LogRecords {
							body := record.GetBody().GetStringValue()
							require.NotContains(t, body, "GIT binary patch")
							if diagnostic := patchLogs[fmt.Sprintf("%x", record.SpanId)]; diagnostic != nil && body != "" {
								var verbose bool
								for _, attr := range record.Attributes {
									if attr.Key == telemetry.LogsVerboseAttr {
										verbose = attr.Value.GetBoolValue()
									}
								}
								require.True(t, verbose, "patch diagnostics must be excluded from tool-result logs")
								key := logKey{fmt.Sprintf("%x", record.SpanId), body, record.TimeUnixNano}
								if !seenLogs[key] {
									diagnostic.WriteString(body)
									seenLogs[key] = true
								}
							}
						}
					}
				}
			}
			seenPrefix, seenTruncation := false, false
			for _, diagnostic := range patchLogs {
				text := diagnostic.String()
				seenPrefix = seenPrefix || strings.Contains(text, "diff --git")
				seenTruncation = seenTruncation || strings.Contains(text, "bytes truncated; read Changeset.asPatch")
				// Allow the command diagnostic and truncation marker in addition
				// to the 256 KiB patch prefix, across all emitted log records.
				require.Less(t, len(text), (256<<10)+1024)
			}
			require.Equal(t, tc.generatePatch, seenPrefix)
			require.Equal(t, tc.name == "long line", seenTruncation)
		})
	}
}

// TestLargeChangesetToolSkipsPatchWork covers a move with both additions and
// removals: the tool-result summary must not compute full paths, which would
// stage every file for rename detection. The recorded overlay is still
// normalized to a patch: keeping a large changeset raw would make restoring
// the conversation re-run the tool that produced it (e.g. a generator).
func (LLMSuite) TestLargeChangesetToolSkipsPatchWork(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	source := c.Directory().
		WithNewFile("dagger.toml", "[modules.editor]\nsource = \"modules/editor\"\n").
		WithNewFile("modules/editor/dagger.json", `{"name":"editor","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("modules/editor/main.dang", `
type Editor {
  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode)
  }

  moveTree(ws: Workspace!): Changeset! {
    let root = ws.directory("/")
    root.withDirectory("new", root.directory("old")).withoutDirectory("old").changes(root)
  }
}
`)
	for i := range 110 {
		source = source.WithNewFile(fmt.Sprintf("old/%03d.txt", i), fmt.Sprintf("file %d\n", i))
	}
	ws := source.AsWorkspace()
	model := cannedRecordingModel(ctx, t, c, c.LLM().
		WithPrompt("move the tree").
		WithResponse([]dagger.LLMContentBlockInput{{
			Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "moveTree",
		}}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "done"}}))
	base := c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws)
	composed, err := composeArtifactAgents(ctx, c, ws, nil, base)
	require.NoError(t, err)
	result := composed.WithPrompt("move the tree").Loop()
	transcript, err := result.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "exceeds the 200-path inspection budget")

	id, err := sink.captureLLMRecipe(ctx, t, c, result)
	require.NoError(t, err)
	gid := new(call.ID)
	require.NoError(t, gid.Decode(string(id)))
	fields := map[string]bool{}
	collectIDFieldNames(gid, fields)
	require.True(t, fields["withPatchFile"], "oversized changesets must be patch-normalized too")
	require.False(t, fields["moveTree"], "the recorded overlay must not retain the tool call")

	entries, err := result.Workspace().Directory("new").Entries(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 110)
	entries, err = result.Workspace().Directory("/").Entries(ctx)
	require.NoError(t, err)
	require.NotContains(t, entries, "old/")
}

// TestChangesetToolPrunesExecution covers commands such as `go test`: they
// execute successfully but produce no file patch. No-ops and directory-only
// changes must drop the execution, without mistaking file mode edits for no-ops.
func (LLMSuite) TestChangesetToolPrunesExecution(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name, edit string
		parallel   bool
	}{
		{name: "no-op", edit: "true"},
		{name: "parallel no-ops", edit: "true", parallel: true},
		{name: "directories only", edit: "mkdir -p added/empty; chmod 700 added/empty; rmdir removed"},
		{name: "file mode only", edit: "chmod +x unchanged.txt"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c, sink := connectWithTrace(ctx, t)
			source := c.Directory().
				WithNewDirectory("removed").
				WithNewFile("unchanged.txt", "keep me\n").
				WithNewFile("dagger.toml", "[modules.runner]\nsource = \"modules/runner\"\n").
				WithNewFile("modules/runner/dagger.json", `{"name":"runner","engineVersion":"v1.0.0-0","sdk":"dang"}`).
				WithNewFile("modules/runner/main.dang", fmt.Sprintf(`
type Runner {
  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode)
  }

  @cache(policy: FunctionCachePolicy.Never)
  run(ws: Workspace!, label: String!): Changeset! {
    let before = container.from("alpine:3.22")
      .withMountedCache("/counter", cacheVolume(%q))
      .withWorkdir("/workspace")
      .withDirectory(".", ws.directory("/", gitignore: true))
      .withEnvVariable("LABEL", label)
      .withEnvVariable("CACHEBUST", UUID.v7)
    let after = before.withExec(["sh", "-ec", %q]).sync
    after.directory(".").withoutDirectory(".git")
      .changes(before.directory(".").withoutDirectory(".git"))
  }
}
`, "no-op-replay-"+identity.NewID(), `test ! -f /counter/"$LABEL" || { echo producer-replayed >&2; exit 91; }; touch /counter/"$LABEL"; `+tc.edit))
			calls := []dagger.LLMContentBlockInput{{
				Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "run",
				Arguments: dagger.JSON(`{"label":"first"}`),
			}}
			if tc.parallel {
				calls = append(calls, dagger.LLMContentBlockInput{
					Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "run",
					Arguments: dagger.JSON(`{"label":"second"}`),
				})
			}
			recording := c.LLM().WithPrompt("run the command").WithResponse(calls)
			for _, call := range calls {
				recording = recording.WithToolResult(call.CallID, "", false)
			}
			model := cannedRecordingModel(ctx, t, c, recording.WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))
			ws := source.AsWorkspace()
			base := c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws)
			composed, err := composeArtifactAgents(ctx, c, ws, nil, base)
			require.NoError(t, err)
			result := composed.WithPrompt("run the command").Loop()
			transcript, err := result.Transcript(ctx)
			require.NoError(t, err)
			require.Contains(t, transcript, "done")
			require.NotContains(t, transcript, "producer-replayed")

			recipe, err := sink.captureLLMRecipe(ctx, t, c, result)
			require.NoError(t, err)
			id := new(call.ID)
			require.NoError(t, id.Decode(string(recipe)))
			fields := map[string]bool{}
			collectIDFieldNames(id, fields)
			require.False(t, fields["run"], "the tool result must not remain a recipe dependency")
			require.False(t, fields["withExec"], "neither After nor Before may retain a command")
			if tc.edit == "true" {
				require.False(t, fields["withChanges"], "no-op commands must not advance workspace state")
			}

			// End the producing session before rebuilding the committed LLM. The
			// cache-mounted sentinel rejects an actual replay of either command.
			require.NoError(t, c.Close())
			target := connect(ctx, t)
			restored := dagger.Ref[*dagger.LLM](target, recipe)
			got, err := restored.Workspace().File("unchanged.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "keep me\n", got)
			entries, err := restored.Workspace().Directory("/").Entries(ctx)
			require.NoError(t, err)
			if tc.name == "directories only" {
				require.NotContains(t, entries, "removed/")
				empty, err := restored.Workspace().Directory("added/empty").Entries(ctx)
				require.NoError(t, err)
				require.Empty(t, empty)
				stat, err := restored.Workspace().Directory("/").Stat(ctx, "added/empty")
				require.NoError(t, err)
				permissions, err := stat.Permissions(ctx)
				require.NoError(t, err)
				require.Equal(t, 0o700, permissions)
			} else {
				require.Contains(t, entries, "removed/")
				require.NotContains(t, entries, "added/")
			}
			if tc.name == "file mode only" {
				stat, err := restored.Workspace().Directory("/").Stat(ctx, "unchanged.txt")
				require.NoError(t, err)
				permissions, err := stat.Permissions(ctx)
				require.NoError(t, err)
				require.Equal(t, 0o111, permissions&0o111, "the executable-bit edit must not be treated as a no-op")
			}
			transcript, err = restored.Transcript(ctx)
			require.NoError(t, err)
			require.Contains(t, transcript, "run")
			require.Contains(t, transcript, "done")
		})
	}
}

// TestChangesetToolKeepsEmptyDirectories locks in that a Changeset-returning
// tool's empty directories survive the engine's patch normalization
// (core.normalizeChangesetToPatch). Git patches carry file content only, so
// without the directory reconciliation the normalized changeset — which
// replaces the original on the live workspace binding — would silently drop
// the empty directory while keeping the file beside it.
func (LLMSuite) TestChangesetToolKeepsEmptyDirectories(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	// Use the existing remote-backed snapshot contract, not session-local Git
	// history, when asserting portable normalization of the tool's changeset.
	base := checkpointCheckoutBase(ctx, t, c).
		With(withWorkspaceFixture(t, c, ".", "workspaces/workspace-tool-return")).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "fixture"})

	model := cannedRecordingModel(ctx, t, c, c.LLM().
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
		// discriminates: a normalized overlay is withPatchFile plus the
		// withNewDirectory that restored the empty directory, while the raw
		// changeset's chain has the tool's operations and no withPatchFile.
		out, err := sink.captureShellRecipe(ctx, t, base, fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace | snapshot) | with-tools $(swapper) | with-prompt "scaffold the project" | loop`,
			model,
		))
		require.NoError(t, err)

		gid := new(call.ID)
		require.NoError(t, gid.Decode(strings.TrimSpace(out)))
		fields := map[string]bool{}
		collectIDFieldNames(gid, fields)
		require.True(t, fields["withPatchFile"],
			"the recorded overlay must be patch-normalized, not the raw changeset")
		require.True(t, fields["withNewDirectory"],
			"the reconciliation must record the empty directory's restoration")
	})
}

// TestChangesetToolsApplyInOrder locks in that a turn's Changeset-returning
// calls take effect in the order the model wrote them (MCP.CallBatch): each
// is applied before the next one runs, so later calls see earlier ones'
// edits, and a call whose changes can't be evaluated fails on its own —
// keeping the edits before it, and the calls after it still run.
func (LLMSuite) TestChangesetToolsApplyInOrder(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-tool-return")

	// batchModel scripts one turn that calls the given tools in one batch.
	batchModel := func(ctx context.Context, t *testctx.T, tools ...string) string {
		var calls []dagger.LLMContentBlockInput
		var results []string
		for i, tool := range tools {
			callID := fmt.Sprintf("call_%d", i+1)
			calls = append(calls, dagger.LLMContentBlockInput{
				Kind: dagger.LLMContentBlockKindToolCall, CallID: callID, ToolName: tool,
			})
			results = append(results, callID)
		}
		llm := c.LLM().WithPrompt("make the changes").WithResponse(calls)
		for _, callID := range results {
			llm = llm.WithToolResult(callID, "", false)
		}
		return cannedRecordingModel(ctx, t, c, llm.WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))
	}
	loopThen := func(ctx context.Context, model, then string) (string, error) {
		return base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "make the changes" | loop | %s`,
			model, then,
		))).Stdout(ctx)
	}

	t.Run("the edit written last wins", func(ctx context.Context, t *testctx.T) {
		// clashFirst and clashSecond rewrite the same lines of shared.txt and
		// both add NEW.txt. clashSecond runs on the tree clashFirst left.
		model := batchModel(ctx, t, "clashFirst", "clashSecond")

		shared, err := loopThen(ctx, model, "workspace | file shared.txt | contents")
		require.NoError(t, err)
		require.Equal(t, "line1: BLUE\nline2: BLUE\n", shared)

		added, err := loopThen(ctx, model, "workspace | file NEW.txt | contents")
		require.NoError(t, err)
		require.Equal(t, "blue new file\n", added)

		// clashSecond's patch is against clashFirst's result, not the
		// original tree.
		transcript, err := loopThen(ctx, model, "transcript")
		require.NoError(t, err)
		require.Contains(t, transcript, "-line1: RED")
	})

	t.Run("a removal written after an edit removes the edited file", func(ctx context.Context, t *testctx.T) {
		model := batchModel(ctx, t, "clashFirst", "removeShared")
		_, err := loopThen(ctx, model, "workspace | file shared.txt | contents")
		require.Error(t, err)
	})

	t.Run("an unevaluable edit fails on its own", func(ctx context.Context, t *testctx.T) {
		// breakShared's search string occurs twice in shared.txt; the
		// replacement is lazy, so only evaluating its changeset finds out.
		model := batchModel(ctx, t, "addFirst", "breakShared", "addSecond")

		transcript, err := loopThen(ctx, model, "transcript")
		require.NoError(t, err)
		require.Contains(t, transcript, "found multiple times")
		require.NotContains(t, transcript, "not run")
		require.Contains(t, transcript, "done")

		// The edit before it landed...
		first, err := loopThen(ctx, model, "workspace | file FIRST.txt | contents")
		require.NoError(t, err)
		require.Equal(t, "first parallel change", strings.TrimSpace(first))
		// ...the broken one did not...
		shared, err := loopThen(ctx, model, "workspace | file shared.txt | contents")
		require.NoError(t, err)
		require.Equal(t, "line1: placeholder\nline2: placeholder\n", shared)
		// ...and the one after it still ran, on the tree the failure left.
		second, err := loopThen(ctx, model, "workspace | file SECOND.txt | contents")
		require.NoError(t, err)
		require.Equal(t, "second parallel change", strings.TrimSpace(second))
	})
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
	model := cannedRecordingModel(ctx, t, c, c.LLM().
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

	t.Run("a swap that builds on the current workspace shows the model a patch", func(ctx context.Context, t *testctx.T) {
		// The returned workspace is the bound one plus an edit (same base), so
		// the tool result is the patch of that edit — not a replacement notice.
		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "swap the workspace" | loop | transcript`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "SWAPPED.txt")
		require.Contains(t, out, "+swapped by tool")
		require.NotContains(t, out, "Workspace replaced")
		require.NotContains(t, out, "Workspace moved")
	})
}

// TestWorkspaceMountSummary keeps attachments and Git metadata out of tool
// results while preserving ordinary root-relative edits from a nested cwd.
func (LLMSuite) TestWorkspaceMountSummary(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name, workspace, edit, notice string
	}{
		{
			name: "directory mount", workspace: "current-workspace",
			edit:   `.withNewFile("/mnt/deps/shadowed.txt", "shadowed payload").withMountedDirectory("/mnt/deps", directory.withNewFile("attachment-only.txt", "mounted payload"))`,
			notice: "Mounted (read-only): mnt/deps",
		},
		{
			name: "file mount", workspace: "current-workspace",
			edit:   `.withNewFile("/mounted.txt", "shadowed payload").withMountedFile("/mounted.txt", directory.withNewFile("attachment-only.txt", "mounted payload").file("attachment-only.txt"))`,
			notice: "Mounted (read-only): mounted.txt",
		},
		{
			name:      "unmount",
			workspace: `current-workspace | with-mounted-directory --path /mnt/deps --source $(directory | with-new-file attachment-only.txt "mounted payload")`,
			edit:      `.withoutMount("/mnt/deps").withNewFile("/mnt/deps/shadowed.txt", "shadowed payload")`, notice: "Unmounted: mnt/deps",
		},
		{
			name:      "replace mount",
			workspace: `current-workspace | with-mounted-directory --path /mnt/deps --source $(directory | with-new-file attachment-only.txt "old mounted payload")`,
			edit:      `.withMountedDirectory("/mnt/deps", directory.withNewFile("replacement-only.txt", "mounted payload"))`,
		},
		{
			name: "git metadata", workspace: "current-workspace | directory / | as-workspace",
			edit: `.withoutDirectory("/.git")`,
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			base := workspaceFixture(t, c, "workspace-tool-return").
				WithNewFile("nested/keep.txt", "keep").
				WithNewFile(".dagger/modules/swapper/main.dang", fmt.Sprintf(`
type Swapper {
  swap(ws: Workspace!): Workspace! {
    ws%s.withNewFile("/ROOT.txt", "root edit\n").withNewFile("EDIT.txt", "nested edit\n")
  }
}
`, tc.edit))
			model := cannedRecordingModel(ctx, t, c, c.LLM().
				WithPrompt("swap the workspace").
				WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "swap"}}).
				WithToolResult("call_1", "", false).
				WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "done"}}))
			out, err := base.With(daggerShell(fmt.Sprintf(`
ws=$(%s | with-workdir nested)
llm --model="%s" | with-workspace --workspace $ws | with-tools $(swapper) | with-prompt "swap the workspace" | loop | transcript
`, tc.workspace, model))).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "diff --git a/ROOT.txt b/ROOT.txt")
			require.Contains(t, out, "diff --git a/nested/EDIT.txt b/nested/EDIT.txt")
			require.Contains(t, out, "+root edit")
			require.Contains(t, out, "+nested edit")
			if tc.notice != "" {
				require.Contains(t, out, tc.notice)
			} else {
				require.NotContains(t, out, "Mounted (read-only):")
				require.NotContains(t, out, "Unmounted:")
			}
			require.NotContains(t, out, "attachment-only.txt")
			require.NotContains(t, out, "replacement-only.txt")
			require.NotContains(t, out, "mounted payload")
			require.NotContains(t, out, "shadowed.txt")
			require.NotContains(t, out, "shadowed payload")
			require.NotContains(t, out, ".git/")
			require.NotContains(t, out, "GIT binary patch")
			require.NotContains(t, out, "WARNING:")
		})
	}
}

// TestToolReturningUnrelatedWorkspaceIsNotDiffed locks in the other half of
// the rebinding's visibility rule: a tool may bind ANY workspace, but what the
// model is told depends on how it relates to the one it replaces
// (core.WorkspaceRelation). A workspace with no base or origin in common gets a
// one-line replacement notice — diffing it against the old one would upload
// the old tree in full and produce a patch describing nothing actionable.
func (LLMSuite) TestToolReturningUnrelatedWorkspaceIsNotDiffed(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-tool-return")

	model := cannedRecordingModel(ctx, t, c, c.LLM().
		WithPrompt("swap to an unrelated workspace").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "swapToUnrelated"},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))
	loopThen := func(ctx context.Context, t *testctx.T, then string) string {
		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "swap to an unrelated workspace" | loop | %s`,
			model, then,
		))).Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	t.Run("the unrelated workspace is still adopted", func(ctx context.Context, t *testctx.T) {
		require.Equal(t, "from an unrelated workspace", strings.TrimSpace(loopThen(ctx, t, "workspace | file OTHER.txt | contents")))
	})

	t.Run("the model sees a replacement notice, not a patch", func(ctx context.Context, t *testctx.T) {
		transcript := loopThen(ctx, t, "transcript")
		require.Contains(t, transcript, "Workspace replaced: file://")
		require.Contains(t, transcript, "-> directory://")
		require.Contains(t, transcript, "Files were not diffed")
		// Neither side's content was rendered: no patch of the new tree's
		// files, and none of the old tree's removals.
		require.NotContains(t, transcript, "diff --git")
		require.NotContains(t, transcript, "+from an unrelated workspace")
		require.NotContains(t, transcript, "-line1: placeholder")
	})
}

// TestToolReturningSameOriginWorkspaceReportsDistance exercises the real log
// selectors used by the move summary, not just rendering precomputed counts.
func (LLMSuite) TestToolReturningSameOriginWorkspaceReportsDistance(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-tool-return").
		WithNewFile(".dagger/modules/swapper/main.dang", `
type Swapper {
  swap(ws: Workspace!): Workspace! {
    ws.git.head.asRepository.branch("new").asWorkspace
  }
}
`).
		WithExec([]string{"sh", "-ec", `
			git add . && git commit -m A
			git checkout -b old
			echo B > revision.txt && git add revision.txt && git commit -m B
			git checkout -b new HEAD~1
			echo C > revision.txt && git add revision.txt && git commit -m C
			echo D > revision.txt && git add revision.txt && git commit -m D
			git checkout old
		`})
	gitDaemon := base.
		WithExec([]string{"apk", "add", "git-daemon"}).
		WithExec([]string{"git", "clone", "--bare", "/work", "/srv/repo.git"}).
		WithExposedPort(9418).
		WithDefaultArgs([]string{"git", "daemon", "--export-all", "--base-path=/srv"}).
		AsService()
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)
	base = base.WithServiceBinding(gitHost, gitDaemon)

	// old: A -> B; new: A -> C -> D. Both directions must be counted:
	// merely adopting the workspace or reporting "Workspace moved" also
	// succeeds when the best-effort log calls fail and omit the counts.
	model := cannedRecordingModel(ctx, t, c, c.LLM().
		WithPrompt("swap the workspace").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "swap"},
		}).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))

	loopThen := func(ctx context.Context, t *testctx.T, then string) string {
		t.Helper()
		out, err := base.With(daggerShell(fmt.Sprintf(`
repo=$(git git://%s/repo.git)
ws=$($repo | branch old | as-workspace)
llm --model="%s" | with-workspace --workspace $ws | with-tools $(swapper) | with-prompt "swap the workspace" | loop | %s
`, gitHost, model, then))).Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	t.Run("the new revision is adopted", func(ctx context.Context, t *testctx.T) {
		require.Equal(t, "D", strings.TrimSpace(loopThen(ctx, t, "workspace | file revision.txt | contents")))
	})

	t.Run("the model sees ahead and behind counts, not a patch", func(ctx context.Context, t *testctx.T) {
		transcript := loopThen(ctx, t, "transcript")
		require.Contains(t, transcript, "Workspace moved:")
		require.Contains(t, transcript, "2 commit(s) ahead of and 1 behind")
		require.Contains(t, transcript, "Files were not diffed")
		require.NotContains(t, transcript, "Workspace replaced:")
		require.NotContains(t, transcript, "diff --git")
	})
}

// TestToolReturningLLMContinues locks in the continuation ring of the state-return
// convention: a tool that returns an LLM replaces the conversation, and the loop
// resumes from the returned one (routeObjectMethodResult -> applyStateReturn ->
// adoptLLM). MCP passes the current conversation directly to the tool's hidden
// LLM! argument, including the tool call itself, so `install`/`reload`-style
// plain transform.
func (LLMSuite) TestToolReturningLLMContinues(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-tool-return")

	t.Run("the loop resumes from the returned conversation", func(ctx context.Context, t *testctx.T) {
		model := cannedRecordingModel(ctx, t, c, c.LLM().
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
		model := cannedRecordingModel(ctx, t, c, c.LLM().
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
		// The continuation bound a different workspace than it was handed, so
		// the model is shown what changed, as it would be for a Workspace swap.
		require.Contains(t, out, "Workspace changed:")
		require.Contains(t, out, "CONTINUED.txt")
		require.Contains(t, out, "done")
	})

	t.Run("a conversation that replaces the current one is adopted", func(ctx context.Context, t *testctx.T) {
		model := cannedRecordingModel(ctx, t, c, c.LLM().
			WithPrompt("start fresh").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "startFresh"},
			}).
			WithToolResult("call_1", "", false).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		// The adopted conversation consumes a *second* recording: after adoption its
		// history is not the one the outer script recorded, but the engine's
		// degraded continuation notice (toolResultSelectors' plain-message arm,
		// carrying summarizeContinuation's summary as the tool result). The
		// fixture's startFresh points the fresh conversation at this model, read
		// from the workspace file written below. If summarizeContinuation's
		// wording — or the swapper's tool count — changes, this string has to
		// change with it; the recorded-response provider compares message text exactly.
		continued := strings.Join([]string{
			"[continued via tool startFresh]",
			"Continuing from the returned conversation.",
			"Toolset unchanged (22 tools).",
			"Conversation history replaced: 2 messages -> 0 messages.",
		}, "\n")
		continuationModel := cannedRecordingModel(ctx, t, c, c.LLM().
			WithPrompt(continued).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		// startFresh wipes the history it was handed. There is no lineage requirement, so
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
		model := cannedRecordingModel(ctx, t, c, c.LLM().
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

	// Continuations run after the turn's other calls, on the state those calls
	// produced (MCP.SplitContinuationCalls), so `[edit, reload]` reloads the
	// edit. That holds whichever order the model emitted them in: the
	// continuation receives a conversation with addFirst's changeset already
	// overlaid and adds its marker on top. Both files must survive, and neither
	// call may be refused.
	loopThen := func(ctx context.Context, t *testctx.T, prompt, model, then string) string {
		t.Helper()
		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "%s" | loop | %s`,
			model, prompt, then,
		))).Stdout(ctx)
		require.NoError(t, err)
		return out
	}
	for _, tc := range []struct {
		name  string
		calls []dagger.LLMContentBlockInput
	}{
		{
			name: "an edit emitted before the continuation is carried into it",
			calls: []dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "addFirst"},
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "continueWithMarker"},
			},
		},
		{
			name: "an edit emitted after the continuation is carried into it too",
			calls: []dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "continueWithMarker"},
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "addFirst"},
			},
		},
		{
			name: "a returned Workspace feeds the continuation",
			calls: []dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "swap"},
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "continueWithMarker"},
			},
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			model := cannedRecordingModel(ctx, t, c, c.LLM().
				WithPrompt("edit and continue").
				WithResponse(tc.calls).
				WithToolResult("call_1", "", false).
				WithToolResult("call_2", "", false).
				WithResponse([]dagger.LLMContentBlockInput{
					{Kind: dagger.LLMContentBlockKindText, Text: "done"},
				}))

			transcript := loopThen(ctx, t, "edit and continue", model, "transcript")
			require.NotContains(t, transcript, "already ran this turn")
			require.Contains(t, transcript, "Continuing from the returned conversation.")
			require.Contains(t, transcript, "done")

			require.Equal(t, "swapped by continuation", strings.TrimSpace(
				loopThen(ctx, t, "edit and continue", model, "workspace | file CONTINUED.txt | contents")))
			edited, editedContents := "FIRST.txt", "first parallel change"
			if tc.calls[0].ToolName == "swap" {
				edited, editedContents = "SWAPPED.txt", "swapped by tool"
			}
			require.Equal(t, editedContents, strings.TrimSpace(
				loopThen(ctx, t, "edit and continue", model, "workspace | file "+edited+" | contents")))
		})
	}

	t.Run("a continuation reached out of order refuses the work it would drop", func(ctx context.Context, t *testctx.T) {
		// Wrapping the continuation in the Timeout builtin runs it in its
		// written position instead of last, so the changeset call after it
		// runs on a workspace the adopted conversation will never see. The
		// changeset call is refused rather than silently dropped, the model is
		// told to re-issue it, and the loop carries on from the continuation.
		model := cannedRecordingModel(ctx, t, c, c.LLM().
			WithPrompt("continue then edit").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "Timeout",
					Arguments: dagger.JSON(`{"duration":"1m","tool":"continueWithMarker","arguments":{}}`)},
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_2", ToolName: "addFirst"},
			}).
			WithToolResult("call_1", "", false).
			WithToolResult("call_2", "", true).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		transcript := loopThen(ctx, t, "continue then edit", model, "transcript")
		require.Contains(t, transcript, "Continuing from the returned conversation.")
		require.Contains(t, transcript, "re-issue this call in the next turn")
		require.Contains(t, transcript, "done")

		require.Equal(t, "swapped by continuation", strings.TrimSpace(
			loopThen(ctx, t, "continue then edit", model, "workspace | file CONTINUED.txt | contents")))
		_, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper) | with-prompt "continue then edit" | loop | workspace | file FIRST.txt | contents`,
			model,
		))).Stdout(ctx)
		require.Error(t, err, "the refused changeset must not have landed")
	})

	t.Run("the base workspace does not already contain the marker", func(ctx context.Context, t *testctx.T) {
		_, err := base.With(daggerShell(
			`current-workspace | file CONTINUED.txt | contents`,
		)).Stdout(ctx)
		require.Error(t, err)
	})
}

// TestReloadedToolsSurviveStateReturns exercises an actual module source change:
// same-type state returns must retain the reloaded binding's defining schema.
func (LLMSuite) TestReloadedToolsSurviveStateReturns(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const modulePath = ".dagger/modules/swapper/main.dang"
	const source = `
type Swapper {
  let state: Int! = 0

  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode)
  }

  reload(llm: LLM!): LLM! {
    let ws = llm.workspace.withNewFile("` + modulePath + `", llm.workspace.file("next-source.txt").contents)
    llm.withWorkspace(ws).compose(expertise: ws.artifacts.filterTypes(["Expertise"]).asExpertise.{{ id }}.map { node(id: _.id).{{ ... on Expertise! }} })
  }

  advance: Swapper! {
    state += 1
    self
  }
%s
}
`
	initialSource := fmt.Sprintf(source, "")
	reloadedSource := fmt.Sprintf(source, `
  added: String! {
    "new tool state: " + toString(state)
  }
`)
	base := workspaceFixture(t, c, "workspace-tool-return").
		WithNewFile(modulePath, initialSource).
		WithNewFile("next-source.txt", reloadedSource)

	toolCall := func(id, name string) dagger.LLMContentBlockInput {
		return dagger.LLMContentBlockInput{Kind: dagger.LLMContentBlockKindToolCall, CallID: id, ToolName: name}
	}
	timeout := func(id string) dagger.LLMContentBlockInput {
		block := toolCall(id, "Timeout")
		block.Arguments = dagger.JSON(`{"duration":"1m","tool":"added","arguments":{}}`)
		return block
	}
	calls := []dagger.LLMContentBlockInput{
		toolCall("reload", "reload"),
		toolCall("before_state", "added"),
		toolCall("advance", "advance"),
		toolCall("after_state", "added"),
		timeout("timeout"),
	}
	nextCalls := []dagger.LLMContentBlockInput{
		toolCall("next_advance", "advance"),
		timeout("next_timeout"),
		toolCall("next_direct", "added"),
	}
	script := c.LLM().WithPrompt("reload and use the new tool")
	for _, block := range calls {
		script = script.WithResponse([]dagger.LLMContentBlockInput{block}).WithToolResult(block.CallID, "", false)
	}
	script = script.WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "first turn done"}}).
		WithPrompt("use it again")
	for _, block := range nextCalls {
		script = script.WithResponse([]dagger.LLMContentBlockInput{block}).WithToolResult(block.CallID, "", false)
	}
	script = script.WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "second turn done"}})
	model := cannedRecordingModel(ctx, t, c, script)
	// The shell starts with the original module installed in its schema. A
	// core-only client would not exercise collisions between old and new
	// definitions of the same Swapper type when state is rebound.
	query := fmt.Sprintf(`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(swapper)`, model)
	run := func(query string) string {
		t.Helper()
		out, err := base.With(daggerShell(query)).Stdout(ctx)
		require.NoError(t, err)
		return out
	}
	require.NotContains(t, run(query+" | tools"), "## added\n")
	query += ` | with-prompt "reload and use the new tool"`
	for _, block := range calls {
		query += " | step"
		require.Contains(t, run(query+" | tools"), "## added\n", block.CallID)
	}
	query += " | loop"
	transcript := run(query + " | transcript")
	require.Contains(t, transcript, "Tools added: added")
	require.Contains(t, transcript, "new tool state: 0")
	require.Equal(t, 2, strings.Count(transcript, "new tool state: 1"))
	require.Contains(t, transcript, "first turn done")

	query += ` | with-prompt "use it again" | loop`
	require.Contains(t, run(query+" | tools"), "## added\n")
	transcript = run(query + " | transcript")
	require.Equal(t, 2, strings.Count(transcript, "new tool state: 2"))
	require.Contains(t, transcript, "second turn done")
	require.NotContains(t, transcript, "is not available")
}

// TestBoundToolAddresses covers dag:// addresses at tool dispatch that name a
// module bound as a tool: they resolve in the conversation's scope
// (LLM.artifacts), where the path is evaluated
// from the LIVE bound object, so it sees state a fresh constructor lacks, and
// collection items take their key from the address's dimension query.
func (LLMSuite) TestBoundToolAddresses(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-bound-tool-addresses")

	toolCall := func(id, name, args string) dagger.LLMContentBlockInput {
		return dagger.LLMContentBlockInput{Kind: dagger.LLMContentBlockKindToolCall, CallID: id, ToolName: name, Arguments: dagger.JSON(args)}
	}
	show := func(id, addr string) dagger.LLMContentBlockInput {
		return toolCall(id, "show", fmt.Sprintf(`{"dir":%q}`, addr))
	}
	calls := []struct {
		block   dagger.LLMContentBlockInput
		isError bool
	}{
		{block: toolCall("add", "withMember", `{"name":"a","contents":"hello from the roster"}`)},
		{block: show("plain", "dag://roster/members/dir?member=a")},
		{block: show("typed", "dag+directory://roster/members/dir?member=a")},
		{block: show("wrong_type", "dag+file://roster/members/dir?member=a"), isError: true},
		{block: show("missing", "dag://roster/members/dir?member=nobody"), isError: true},
	}
	script := c.LLM().WithPrompt("show the new member")
	for _, call := range calls {
		script = script.
			WithResponse([]dagger.LLMContentBlockInput{call.block}).
			WithToolResult(call.block.CallID, "", call.isError)
	}
	script = script.WithResponse([]dagger.LLMContentBlockInput{
		{Kind: dagger.LLMContentBlockKindText, Text: "done"},
	})
	model := cannedRecordingModel(ctx, t, c, script)

	t.Run("the tool schema advertises tool module addresses", func(ctx context.Context, t *testctx.T) {
		tools, err := base.With(daggerShell("llm | with-tools $(roster) | tools")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, tools, "## show\n")
		require.Contains(t, tools, "(Directory address:")
		require.Contains(t, tools, "one of your tool modules (with its current state)")
	})

	t.Run("addresses resolve against the bound object's state", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(roster) | with-prompt "show the new member" | loop | transcript`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)
		// Both the plain and the type-asserted address reach the member the
		// model added, which exists only in the bound roster.
		require.Equal(t, 2, strings.Count(out, "shown: hello from the roster"), out)
		// A type assertion that does not match reports like a workspace
		// artifact's...
		require.Contains(t, out, "is a Directory, not file")
		// ...and an unknown key does not fall back to a fresh roster.
		require.Contains(t, out, `resolve "dag://roster/members/dir?member=nobody": no artifact matches`)
		require.Contains(t, out, "done")
	})

	t.Run("workspace resolution constructs a fresh roster", func(ctx context.Context, t *testctx.T) {
		// The same address through Workspace.resolve evaluates a fresh
		// Roster, which has no members.
		_, err := base.With(daggerShell(
			`current-workspace | resolve "dag://roster/members/dir?member=a" | directory | entries`,
		)).Stdout(ctx)
		requireErrOut(t, err, `resolve "dag://roster/members/dir?member=a": no artifact matches`)
	})

	t.Run("selection args take addresses in the conversation's scope", func(ctx context.Context, t *testctx.T) {
		// Artifacts and Artifact args lift a DAG address — scheme optional,
		// the path a glob — into the part of the conversation's scope it
		// selects, which the module function then evaluates itself.
		const prompt = "read the notes"
		const notes = "hello from the roster | workspace readme"
		script := c.LLM().WithPrompt(prompt).
			// Rebind and read in one turn: the read sees the new member.
			WithResponse([]dagger.LLMContentBlockInput{
				toolCall("add", "withMember", `{"name":"a","contents":"hello from the roster"}`),
				toolCall("all", "readAll", `{"targets":"roster/**/notes"}`),
			}).
			WithToolResult("add", "", false).
			WithToolResult("all", "", false)
		for _, call := range []struct {
			block   dagger.LLMContentBlockInput
			isError bool
		}{
			{block: toolCall("one", "read", `{"target":"roster/members/notes?member=a"}`)},
			{block: toolCall("typed", "read", `{"target":"dag+file://roster/members/notes?member=a"}`)},
			{block: toolCall("many", "read", `{"target":"roster/members/*"}`), isError: true},
		} {
			script = script.
				WithResponse([]dagger.LLMContentBlockInput{call.block}).
				WithToolResult(call.block.CallID, "", call.isError)
		}
		model := cannedRecordingModel(ctx, t, c, script.WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))

		tools, err := base.With(daggerShell("llm | with-tools $(inspector) | tools")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, tools, "## readAll\n")
		require.Contains(t, tools, "(Artifacts address:")
		require.Contains(t, tools, "## read\n")
		require.Contains(t, tools, "(Artifact address:")

		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-workspace --workspace $(current-workspace) | with-tools $(roster) | with-tools $(inspector) | with-prompt "%s" | loop | transcript`,
			model, prompt,
		))).Stdout(ctx)
		require.NoError(t, err)
		// The bare glob selects the bound roster's notes, member a included:
		// the rebinding earlier in the turn is in scope. Evaluated inside the
		// module function, the notes still get the conversation's workspace.
		require.Contains(t, out, "dag://roster/members/notes?member=a: "+notes, out)
		// A keyed address, with or without a type assertion, picks one.
		require.Equal(t, 2, strings.Count(out, "read: "+notes), out)
		// An Artifact must be exactly one: several matches are listed.
		require.Contains(t, out, `"roster/members/*" is not a resolvable Artifact address`)
		require.Contains(t, out, "dag://roster/members/dir?member=a")
		require.Contains(t, out, "FindArtifacts lists what exists")
		require.Contains(t, out, "done")
	})

	t.Run("the LLM scope has the bound tools' live artifacts", func(ctx context.Context, t *testctx.T) {
		const setup = `roster=$(roster | with-member --name a --contents "hello from the roster")
scope=$(llm | with-workspace --workspace $(current-workspace) | with-tools $roster | artifacts)
`
		run := func(ctx context.Context, t *testctx.T, script string) string {
			t.Helper()
			out, err := base.With(daggerShell(setup + script)).Stdout(ctx)
			require.NoError(t, err)
			return out
		}
		// The collection's keys come from the bound roster's state...
		require.Equal(t, "dag://roster/members/dir?member=a\n",
			run(ctx, t, `$scope | filter-uri "dag://roster/members/dir" | items | uri`))
		// ...and so does the value.
		require.Equal(t, "hello from the roster",
			run(ctx, t, `$scope | filter-uri "dag://roster/members/dir?member=a" | one | value | file f | contents`))
		// The workspace's other modules are in scope, constructed fresh.
		require.Equal(t, "dag://notes/docs\n",
			run(ctx, t, `$scope | filter-uri "dag://notes/**" | items | uri`))
		require.Equal(t, "README\n",
			run(ctx, t, `$scope | filter-uri "dag://notes/docs" | one | value | entries`))
		// The workspace's own roster is shadowed by the bound one: the
		// collection is listed once, not once per roster.
		require.Equal(t, "dag://roster/members\n",
			run(ctx, t, `$scope | filter-uri "dag://roster/members" | items | uri`))
	})

	t.Run("the LLM scope's workspace part is the bound workspace's", func(ctx context.Context, t *testctx.T) {
		// A bound workspace's roster is constructed fresh: it has no members.
		out, err := base.With(daggerShell(
			`llm | with-workspace --workspace $(current-workspace) | artifacts | filter-uri "dag://roster/members/dir" | items | uri`,
		)).Stdout(ctx)
		require.NoError(t, err)
		require.Empty(t, out)
		// Without a bound workspace, the scope has no workspace part: the
		// calling client's current workspace is not the conversation's.
		out, err = base.With(daggerShell(
			`llm | artifacts | filter-uri "dag://{notes,roster}/**" | path-definitions | uri`,
		)).Stdout(ctx)
		require.NoError(t, err)
		require.Empty(t, out)
		// Bound tools are in scope all the same.
		out, err = base.With(daggerShell(
			`llm | with-tools $(roster) | artifacts | filter-uri "dag://{notes,roster}/**" | path-definitions | uri`,
		)).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "dag://roster/members\n")
		require.NotContains(t, out, "dag://notes/")
	})
}

// TestBoundCollectionRefs covers agent histories addressed through a bound
// tool's collection, in the shape of vito/agents' staff and committer modules
// (the workspace-agent-refs fixture mirrors them): a never-started chief
// Agent is put on a Roster, and its committed history is addressed as
// dag://roster/members/head?member=chief — which only resolves against the
// BOUND Roster (or a View minted from it), since a fresh one has no members —
// and handed to GitRef-taking tools of another module by the model.
func (LLMSuite) TestBoundCollectionRefs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-agent-refs").
		WithNewFile("README.md", "base\n").
		WithExec([]string{"git", "add", "-A"}).
		WithExec([]string{"git", "commit", "-m", "base"})
	baseSHA, err := base.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	baseSHA = strings.TrimSpace(baseSHA)

	// The chief works in a copy of the workspace with one more commit. It is
	// spawned but never sent a message, so its loop never starts: its
	// snapshot is just the conversation it was spawned with.
	const chiefCommit = "chief: add chief.txt"
	// Snapshot the local workspace up front: only its owning client (this
	// shell) may capture it, and the git tools run as a module.
	const setup = `ws=$(current-workspace | snapshot)
chiefWs=$($ws | with-new-file chief.txt "from the chief")
chiefWs=$($chiefWs | with-commit --changes $($chiefWs | git | uncommitted) --message "` + chiefCommit + `" --date 2026-09-05T12:00:00Z)
chief=$(llm | with-workspace --workspace $chiefWs | spawn --name chief)
roster=$(roster | with-worker --name chief --worker $chief)
`
	run := func(ctx context.Context, t *testctx.T, script string) string {
		t.Helper()
		out, err := base.With(daggerShell(setup + script)).Stdout(ctx)
		require.NoError(t, err)
		return out
	}
	// The commit is pinned (fixed date and identity), so every run of the
	// setup produces the same chief commit.
	chiefSHA := strings.TrimSpace(run(ctx, t, `$chiefWs | git | head | commit-sha`))
	require.NotEqual(t, baseSHA, chiefSHA)

	toolCall := func(id, name, args string) dagger.LLMContentBlockInput {
		return dagger.LLMContentBlockInput{Kind: dagger.LLMContentBlockKindToolCall, CallID: id, ToolName: name, Arguments: dagger.JSON(args)}
	}
	const chiefHead = "dag://roster/members/head?member=chief"
	conversation := func(prompt string, calls ...dagger.LLMContentBlockInput) string {
		script := c.LLM().WithPrompt(prompt)
		for _, call := range calls {
			script = script.
				WithResponse([]dagger.LLMContentBlockInput{call}).
				WithToolResult(call.CallID, "", false)
		}
		return cannedRecordingModel(ctx, t, c, script.WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))
	}
	chat := func(model, tools, prompt string) string {
		return fmt.Sprintf(`llm --model="%s" | with-workspace --workspace $ws | with-tools %s | with-tools $(git-tools) | with-prompt "%s" | loop`, model, tools, prompt)
	}

	t.Run("the bound roster resolves the address", func(ctx context.Context, t *testctx.T) {
		const prompt = "look at the chief's work"
		model := conversation(prompt,
			toolCall("plain", "show", fmt.Sprintf(`{"from":%q}`, chiefHead)),
			toolCall("typed", "show", `{"from":"dag+git-ref://roster/members/head?member=chief"}`),
		)
		transcript := run(ctx, t, chat(model, "$roster", prompt)+" | transcript")
		// Both spellings reach the chief's HEAD, which only the bound roster
		// knows.
		require.Equal(t, 2, strings.Count(transcript, "commit "+chiefSHA), transcript)
		require.Equal(t, 2, strings.Count(transcript, chiefCommit), transcript)
		require.Contains(t, transcript, "+from the chief")
	})

	t.Run("a view of the roster roots the address", func(ctx context.Context, t *testctx.T) {
		// A worker-shaped binding: only the View (not the Roster, the
		// module's main object) is bound, and it has the members field.
		const prompt = "look at the chief's work"
		model := conversation(prompt,
			toolCall("show", "show", fmt.Sprintf(`{"from":%q}`, chiefHead)),
		)
		transcript := run(ctx, t, chat(model, "$($roster | view)", prompt)+" | transcript")
		require.Contains(t, transcript, "commit "+chiefSHA)
		require.Contains(t, transcript, "+from the chief")
	})

	t.Run("FindArtifacts discovers the address", func(ctx context.Context, t *testctx.T) {
		// The model looks for GitRefs, lists the heads' keyed addresses, and
		// passes the chief's to a GitRef-taking tool.
		const prompt = "find the chief's work"
		model := conversation(prompt,
			toolCall("find", "FindArtifacts", `{"type":"GitRef"}`),
			toolCall("items", "FindArtifacts", `{"address":"roster/members/head","view":"items"}`),
			toolCall("show", "show", `{"from":"dag+git-ref://roster/members/head?member=chief"}`),
		)
		transcript := run(ctx, t, chat(model, "$roster", prompt)+" | transcript")
		// The path, with a placeholder for the member it needs, is marked as
		// read from the bound roster...
		require.Contains(t, transcript,
			"dag+git-ref://roster/members/head?member=<name> — The agent's committed history: the HEAD of its workspace. [tool Roster, live]")
		// ...and the items are its live members, fully keyed.
		require.Contains(t, transcript,
			"dag+git-ref://roster/members/head?member=chief — The agent's committed history: the HEAD of its workspace. [tool Roster, live]")
		require.Contains(t, transcript, "commit "+chiefSHA)
		require.Contains(t, transcript, "+from the chief")
	})

	t.Run("an LLM-returning tool advances the bound workspace", func(ctx context.Context, t *testctx.T) {
		// adopt returns an LLM: a continuation the loop resumes from, with
		// the chief's commits in its workspace.
		const prompt = "adopt the chief's work"
		model := conversation(prompt,
			toolCall("adopt", "adopt", fmt.Sprintf(`{"ref":%q}`, chiefHead)),
		)
		transcript := run(ctx, t, chat(model, "$roster", prompt)+" | transcript")
		require.Contains(t, transcript, "Continuing from the returned conversation.")

		require.Equal(t, chiefSHA, strings.TrimSpace(
			run(ctx, t, chat(model, "$roster", prompt)+" | workspace | git | head | commit-sha")))
		require.Equal(t, "from the chief", strings.TrimSpace(
			run(ctx, t, chat(model, "$roster", prompt)+" | workspace | file chief.txt | contents")))
	})
}

// TestFindArtifacts covers the FindArtifacts builtin over a bound workspace: a
// module that fails to load is listed with its error rather than failing the
// listing, like `dagger check -l`.
func (LLMSuite) TestFindArtifacts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// The shell skips loading the workspace's modules (-M): it cannot load a
	// broken one. Workspace.artifacts still discovers them, best effort.
	//
	// The conversation is bound to a snapshot of the workspace: a value
	// workspace serves only core to the LLM, whereas a live one loads every
	// workspace module strictly (MCP.baseServer), which fails on the broken
	// one before any tool runs. The snapshot needs the fixture committed.
	base := workspaceFixture(t, c, "generators-broken").
		WithExec([]string{"sh", "-c", "git add -A && git commit --allow-empty -m fixture"})
	const bind = `with-workspace --workspace $(current-workspace | snapshot)`

	t.Run("a bound workspace makes it a tool", func(ctx context.Context, t *testctx.T) {
		tools, err := base.With(daggerShellNoMod("llm | " + bind + " | tools")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, tools, "## FindArtifacts\n")
		// An unbound LLM has an empty scope: nothing to find.
		tools, err = base.With(daggerShellNoMod("llm | tools")).Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, tools, "## FindArtifacts\n")
	})

	t.Run("checks and load errors are listed", func(ctx context.Context, t *testctx.T) {
		const prompt = "which checks are there?"
		script := c.LLM().WithPrompt(prompt)
		for i, args := range []string{`{}`, `{"type":"Check"}`} {
			id := fmt.Sprintf("find_%d", i)
			script = script.
				WithResponse([]dagger.LLMContentBlockInput{
					{Kind: dagger.LLMContentBlockKindToolCall, CallID: id, ToolName: "FindArtifacts", Arguments: dagger.JSON(args)},
				}).
				WithToolResult(id, "", false)
		}
		model := cannedRecordingModel(ctx, t, c, script.WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "done"},
		}))
		out, err := base.With(daggerShellNoMod(fmt.Sprintf(
			`llm --model="%s" | %s | with-prompt "%s" | loop | transcript`, model, bind, prompt,
		))).Stdout(ctx)
		require.NoError(t, err)
		// The overview counts the loaded module's artifacts by type and names
		// the module that failed to load.
		require.Contains(t, out, "Check (")
		require.Contains(t, out, "Service (1): good")
		require.Contains(t, out, "Modules that failed to load")
		// The checks: the loaded module's, and the load failure in place of
		// the broken module's.
		require.Contains(t, out, "dag+check://good/verify — A trivial check. Used to prove the module loaded for `dagger check`.")
		require.Contains(t, out, "dag+check://bad/load — LOAD ERROR: ")
		require.NotContains(t, out, "dag+service://good/web —")
		require.Contains(t, out, "done")
	})
}

// TestAddressableToolArgs covers address lifting of object-typed tool args end
// to end: a module function with a required arg of an addressable type still
// becomes a tool — the arg renders as an address string, and a model-supplied
// address is lifted into the real object via the core Address API at
// dispatch — unless the type is blocklisted (Secret, Socket, Volume mint
// capabilities from a string), in which case a required arg still
// disqualifies its function. See liftableObjectArg in
// core/llm_object_tools.go.
func (LLMSuite) TestAddressableToolArgs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "workspace-addressable-args")

	t.Run("required liftable args render as addresses", func(ctx context.Context, t *testctx.T) {
		tools, err := base.With(daggerShell("llm | with-tools $(runner) | tools")).Stdout(ctx)
		require.NoError(t, err)

		// exec IS a tool despite its required Container! arg...
		require.Contains(t, tools, "## exec\n")
		// ...and its sandbox parameter is described as an address — with the
		// type's syntax hint — not as a bare ID.
		require.Contains(t, tools, "(Container address:")
		require.Contains(t, tools, "or a Container ID from a prior tool result")

		// Directory and GitRef are liftable as well.
		require.Contains(t, tools, "## lsDir\n")
		require.Contains(t, tools, "(Directory address:")
		require.Contains(t, tools, "## commitOf\n")
		require.Contains(t, tools, "(GitRef address:")

		// useToken's required Secret! arg is blocklisted, so useToken is not
		// exposed as a tool: an env:// address would mint a secret.
		require.NotContains(t, tools, "## useToken\n")
		require.NotContains(t, tools, "(Secret address:")
	})

	t.Run("a git URL lifts into a real git ref", func(ctx context.Context, t *testctx.T) {
		model := cannedRecordingModel(ctx, t, c, c.LLM().
			WithPrompt("which commit is the tag?").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "commitOf",
					Arguments: dagger.JSON(`{"ref":"https://github.com/dagger/dagger#v0.9.0"}`)},
			}).
			WithToolResult("call_1", "", false).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "done"},
			}))

		out, err := base.With(daggerShell(fmt.Sprintf(
			`llm --model="%s" | with-tools $(runner) | with-prompt "which commit is the tag?" | loop | transcript`,
			model,
		))).Stdout(ctx)
		require.NoError(t, err)
		// The commit SHA only exists in the resolved ref, never in the
		// arguments, so it proves the address lifted into a real GitRef.
		require.Regexp(t, `commit: [0-9a-f]{40}`, out)
	})

	t.Run("an image ref lifts into a real container", func(ctx context.Context, t *testctx.T) {
		model := cannedRecordingModel(ctx, t, c, c.LLM().
			WithPrompt("what OS is the sandbox running?").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "exec",
					Arguments: dagger.JSON(fmt.Sprintf(`{"cmd":["cat","/etc/os-release"],"sandbox":%q}`, alpineImage))},
			}).
			// Placeholder result: the real tool runs while consuming the recording (tool
			// results are excluded from the recorded-response provider's history matching), so
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

		model := cannedRecordingModel(ctx, t, c, c.LLM().
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
