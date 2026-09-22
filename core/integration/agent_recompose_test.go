package core

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

const recomposeModulePath = ".dagger/modules/swapper/main.dang"

// Private fields deliberately cannot be recovered through public GraphQL
// selections. Recomposition must carry their serialized state to the new code.
const recomposeSource = `
type Swapper {
  let state: Int! = 0
  let notes: Map[String!]! = ["seed": "constructor"]

  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode)
  }

  advance: Swapper! {
    state += 1
    if (state == 1) {
      self.notes = notes.with("memo", "kept")
    }
    self
  }

  """Read the original private state."""
  readState: String! {
    "private counter: " + toString(state) + "; notes: " + JSON.encode(notes)
  }

  reload(llm: LLM!): LLM! {
    llm.workspace.agents.recompose(base: llm)
  }
%s
}
`

func recomposeFixture(c *dagger.Client, source string) *dagger.Directory {
	return c.Directory().
		WithNewFile("dagger.toml", "[modules.swapper]\nsource = \".dagger/modules/swapper\"\n").
		WithNewFile(".dagger/modules/swapper/dagger.json", `{"name":"swapper","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile(recomposeModulePath, source)
}

// Use the live schema so this regression does not depend on SDK regeneration.
func recomposeLLM(ctx context.Context, c *dagger.Client, ws *dagger.Workspace, base *dagger.LLM, include ...string) (*dagger.LLM, error) {
	wsID, err := ws.ID(ctx)
	if err != nil {
		return nil, err
	}
	baseID, err := base.WithWorkspace(ws).ID(ctx)
	if err != nil {
		return nil, err
	}
	var res struct {
		Node struct {
			Agents struct {
				Recompose struct{ ID string }
			}
		}
	}
	err = c.Do(ctx, &dagger.Request{
		Query: `query($workspace: ID!, $base: ID!, $include: [String!]) {
			node(id: $workspace) { ... on Workspace { agents(include: $include) { recompose(base: $base) { id } } } }
		}`,
		Variables: map[string]any{"workspace": wsID, "base": baseID, "include": include},
	}, &dagger.Response{Data: &res})
	if err != nil {
		return nil, err
	}
	return dagger.Ref[*dagger.LLM](c, dagger.ID(res.Node.Agents.Recompose.ID)), nil
}

func recomposeTool(id, name string) dagger.LLMContentBlockInput {
	return dagger.LLMContentBlockInput{Kind: dagger.LLMContentBlockKindToolCall, CallID: id, ToolName: name}
}

func recomposeRecordingTurn(llm *dagger.LLM, prompt string, tools ...string) *dagger.LLM {
	llm = llm.WithPrompt(prompt)
	for i, tool := range tools {
		call := recomposeTool(fmt.Sprintf("%s_%d", prompt, i), tool)
		llm = llm.WithResponse([]dagger.LLMContentBlockInput{call}).WithToolResult(call.CallID, "", false)
	}
	return llm.WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: prompt + " done"}})
}

func (LLMSuite) TestRecomposePrivateStateAndReplay(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	initial := fmt.Sprintf(recomposeSource, "")
	updated := strings.ReplaceAll(fmt.Sprintf(recomposeSource, `
  let addedDefault: String! = "new field default"

  """A newly installed method with new documentation."""
  added: String! {
    addedDefault + "; updated counter: " + toString(state)
  }
`), "Read the original private state.", "Read the updated private state.")
	fixture := recomposeFixture(c, initial)
	ws := fixture.AsWorkspace()
	script := recomposeRecordingTurn(c.LLM(), "mutate", "advance", "readState")
	script = recomposeRecordingTurn(script, "unchanged", "readState")
	script = recomposeRecordingTurn(script, "edited", "added", "advance", "added", "readState")
	script = recomposeRecordingTurn(script, "restored", "advance", "added", "readState")
	script = recomposeRecordingTurn(script, "installed", "added", "extraTool", "readState")
	model := cannedRecordingModel(ctx, t, c, script)
	mapPattern := regexp.MustCompile(`"memo"\s*:\s*"kept"`)
	llm := ws.Agents().Compose(dagger.AgentMiddlewareGroupComposeOpts{Base: c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws)}).
		WithPrompt("mutate").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 1")
	require.Regexp(t, `"memo"\s*:\s*"kept"`, transcript)

	llm, err = recomposeLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	llm = llm.WithPrompt("unchanged").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(transcript, "private counter: 1"))
	require.Len(t, mapPattern.FindAllString(transcript, -1), 2)

	// Edit the existing workspace overlay, as the editor does: rebuilding a
	// Directory-backed workspace would give the module a different origin.
	ws = ws.WithNewFile(recomposeModulePath, updated)
	llm, err = recomposeLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	tools, err := llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## added\n")
	require.Contains(t, tools, "A newly installed method with new documentation.")
	require.Contains(t, tools, "Read the updated private state.")
	require.NotContains(t, tools, "Read the original private state.")
	llm = llm.WithPrompt("edited").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "new field default; updated counter: 1")
	require.Contains(t, transcript, "new field default; updated counter: 2")
	require.Contains(t, transcript, "private counter: 2")
	require.Len(t, mapPattern.FindAllString(transcript, -1), 3)

	// Recompose twice, then replay the portable recipe in a fresh client with
	// no module schema served. The next same-type return must retain NEW code.
	for range 2 {
		llm, err = recomposeLLM(ctx, c, ws, llm)
		require.NoError(t, err)
	}
	portable, err := llm.PortableID(ctx)
	require.NoError(t, err)
	target := connect(ctx, t)
	llm = dagger.Ref[*dagger.LLM](target, portable).WithPrompt("restored").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "new field default; updated counter: 3")
	require.Contains(t, transcript, "private counter: 3")
	require.Len(t, mapPattern.FindAllString(transcript, -1), 4)
	require.NotContains(t, transcript, "is not available")

	// Adding a middleware is the install path: initialize only the new module,
	// not the existing module's counter/map or its previously added field.
	installed := llm.Workspace().
		WithNewFile("dagger.toml", "[modules.swapper]\nsource = \".dagger/modules/swapper\"\n[modules.extra]\nsource = \"modules/extra\"\n").
		WithNewFile("modules/extra/dagger.json", `{"name":"extra","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("modules/extra/main.dang", `
type Extra {
  let marker: String! = "installed default"
  agent(base: LLM!): LLM! @agent { base.withTools(currentNode) }
  extraTool: String! { marker }
}
`)
	llm, err = recomposeLLM(ctx, target, installed, llm)
	require.NoError(t, err)
	transcript, err = llm.WithPrompt("installed").Loop().Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "installed default")
	require.Equal(t, 2, strings.Count(transcript, "new field default; updated counter: 3"))
	require.Equal(t, 2, strings.Count(transcript, "private counter: 3"))
	require.Len(t, mapPattern.FindAllString(transcript, -1), 5)
}

func (LLMSuite) TestRecomposeFailureKeepsOldState(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct{ name, source string }{
		{"broken source", "this is not a Dang module"},
		{"incompatible private map", strings.ReplaceAll(strings.ReplaceAll(
			fmt.Sprintf(recomposeSource, ""),
			`let notes: Map[String!]! = ["seed": "constructor"]`, `let notes: Map[Int!]! = ["seed": 42]`),
			`notes.with("memo", "kept")`, `notes.with("memo", 99)`)},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			fixture := recomposeFixture(c, fmt.Sprintf(recomposeSource, ""))
			ws := fixture.AsWorkspace()
			script := recomposeRecordingTurn(c.LLM(), "mutate", "advance")
			script = recomposeRecordingTurn(script, "still usable", "advance", "readState")
			model := cannedRecordingModel(ctx, t, c, script)
			llm := ws.Agents().Compose(dagger.AgentMiddlewareGroupComposeOpts{Base: c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws)}).
				WithPrompt("mutate").Loop()
			_, err := llm.Sync(ctx)
			require.NoError(t, err)
			_, err = recomposeLLM(ctx, c, ws.WithNewFile(recomposeModulePath, tc.source), llm)
			require.Error(t, err, "reload must not silently reset incompatible state")
			if tc.name == "incompatible private map" {
				require.Contains(t, err.Error(), "notes")
			}
			contents, err := llm.Workspace().File(recomposeModulePath).Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf(recomposeSource, ""), contents)
			transcript, err := llm.WithPrompt("still usable").Loop().Transcript(ctx)
			require.NoError(t, err)
			require.Contains(t, transcript, "private counter: 2")
			require.Regexp(t, `"memo"\s*:\s*"kept"`, transcript)
		})
	}
}

func (LLMSuite) TestRecomposeStateVersion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// The same private-map retype that TestRecomposeFailureKeepsOldState rejects,
	// declared incompatible by the author via a state version. Recomposing then
	// resets the object to the new revision's defaults instead of failing.
	retyped := func(source string) string {
		return strings.ReplaceAll(strings.ReplaceAll(source,
			`let notes: Map[String!]! = ["seed": "constructor"]`, `let notes: Map[Int!]! = ["seed": 42]`),
			`notes.with("memo", "kept")`, `notes.with("memo", 99)`)
	}
	withVersion := func(source string) string {
		return strings.ReplaceAll(source, "base.withTools(currentNode)", "base.withTools(currentNode, version: 2)")
	}
	versioned := withVersion(retyped(fmt.Sprintf(recomposeSource, "")))
	extended := withVersion(retyped(fmt.Sprintf(recomposeSource, `
  added: String! { "added; counter: " + toString(state) }
`)))
	fixture := recomposeFixture(c, fmt.Sprintf(recomposeSource, ""))
	ws := fixture.AsWorkspace()
	script := recomposeRecordingTurn(c.LLM(), "mutate", "advance", "readState")
	script = recomposeRecordingTurn(script, "reset", "readState", "advance")
	script = recomposeRecordingTurn(script, "carried", "added", "readState")
	model := cannedRecordingModel(ctx, t, c, script)
	llm := ws.Agents().Compose(dagger.AgentMiddlewareGroupComposeOpts{Base: c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws)}).
		WithPrompt("mutate").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 1")
	require.Regexp(t, `"memo"\s*:\s*"kept"`, transcript)

	// Changing the withTools version from its default of 0 to 2 discards the
	// old state rather than overlaying it onto the retyped map.
	ws = ws.WithNewFile(recomposeModulePath, versioned)
	llm, err = recomposeLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	llm = llm.WithPrompt("reset").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 0")
	require.Regexp(t, `"seed"\s*:\s*42`, transcript)
	require.Len(t, regexp.MustCompile(`"memo"\s*:\s*"kept"`).FindAllString(transcript, -1), 1, "the old memo is gone after the reset")

	// An edit at the same version carries the (now version 2) state over.
	ws = ws.WithNewFile(recomposeModulePath, extended)
	llm, err = recomposeLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	transcript, err = llm.WithPrompt("carried").Loop().Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "added; counter: 1")
	require.Equal(t, 2, strings.Count(transcript, "private counter: 1"))
	require.Regexp(t, `"memo"\s*:\s*99`, transcript)
}

const recomposeGoModulePath = ".dagger/modules/swapper/main.go"

// The same shape as recomposeSource, in a container SDK. Nothing about state
// transfer is SDK-specific: private fields are whatever the SDK serializes.
const recomposeGoSource = `package main

import (
	"encoding/json"
	"fmt"

	"dagger/swapper/internal/dagger"
)

type Swapper struct {
	// An ordinary field, not engine metadata.
	StateVersion string
	// +private
	State int
	// +private
	Notes []string
%s
}

func New() *Swapper {
	return &Swapper{Notes: []string{"seed=constructor"}%s}
}

// +agent
func (s *Swapper) Agent(base *dagger.LLM) *dagger.LLM {
	return base.WithTools(dag.CurrentNode())
}

func (s *Swapper) Advance() *Swapper {
	s.State++
	if s.State == 1 {
		s.Notes = append(s.Notes, "memo=kept")
	}
	return s
}

// Read the private state.
func (s *Swapper) ReadState() string {
	notes, _ := json.Marshal(s.Notes)
	return fmt.Sprintf("private counter: %%d; notes: %%s", s.State, notes)
}
%s
`

func recomposeGoFixture(c *dagger.Client, source string) *dagger.Directory {
	return c.Directory().
		WithNewFile("dagger.toml", "[modules.swapper]\nsource = \".dagger/modules/swapper\"\n").
		WithNewFile(".dagger/modules/swapper/dagger.json", `{"name":"swapper","engineVersion":"v1.0.0-0","sdk":"go"}`).
		WithNewFile(recomposeGoModulePath, source)
}

func (LLMSuite) TestRecomposeGoModuleState(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	initial := fmt.Sprintf(recomposeGoSource, "", "", "")
	extended := fmt.Sprintf(recomposeGoSource, `
	// +private
	Label string`, `, Label: "new field default"`, `
func (s *Swapper) Added() string {
	return fmt.Sprintf("%s; updated counter: %d", s.Label, s.State)
}
`)
	versioned := strings.ReplaceAll(extended, "base.WithTools(dag.CurrentNode())", "base.WithTools(dag.CurrentNode(), dagger.LLMWithToolsOpts{Version: 2})")
	fixture := recomposeGoFixture(c, initial)
	ws := fixture.AsWorkspace()
	script := recomposeRecordingTurn(c.LLM(), "mutate", "advance", "readState")
	script = recomposeRecordingTurn(script, "edited", "added", "advance", "readState")
	script = recomposeRecordingTurn(script, "reset", "readState")
	model := cannedRecordingModel(ctx, t, c, script)
	llm := ws.Agents().Compose(dagger.AgentMiddlewareGroupComposeOpts{Base: c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws)}).
		WithPrompt("mutate").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 1")
	require.Contains(t, transcript, "memo=kept")

	// A new revision with an added private field and method: existing state
	// carries over, the new field takes the new constructor's default.
	ws = ws.WithNewFile(recomposeGoModulePath, extended)
	llm, err = recomposeLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	llm = llm.WithPrompt("edited").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "new field default; updated counter: 1")
	require.Contains(t, transcript, "private counter: 2")
	require.Equal(t, 2, strings.Count(transcript, "memo=kept"))

	// Changing the withTools version resets the object to the new defaults.
	ws = ws.WithNewFile(recomposeGoModulePath, versioned)
	llm, err = recomposeLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	tools, err := llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## stateVersion\n", "a field named stateVersion is an ordinary tool")
	transcript, err = llm.WithPrompt("reset").Loop().Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 0")
	require.Equal(t, 2, strings.Count(transcript, "memo=kept"))
}

func (LLMSuite) TestRecomposeSameBatchStateReturn(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const editSource = `
  updateSource(ws: Workspace!): Workspace! {
    ws.withNewFile("` + recomposeModulePath + `", ws.file("next-source.txt").contents)
  }
`
	initial := fmt.Sprintf(recomposeSource, editSource)
	updated := fmt.Sprintf(recomposeSource, editSource+`
  added: String! { "updated source; counter: " + toString(state) }
`)
	base := workspaceFixture(t, c, "workspace-tool-return").
		WithNewFile(recomposeModulePath, initial).
		WithNewFile("next-source.txt", updated)
	// Deliberately emit reload first. Continuations run last and must see both
	// the source edit and same-type receiver returned by the other calls.
	script := c.LLM().WithPrompt("update and reload").WithResponse([]dagger.LLMContentBlockInput{
		recomposeTool("reload", "reload"), recomposeTool("advance", "advance"), recomposeTool("edit", "updateSource"),
	}).WithToolResult("advance", "", false).WithToolResult("edit", "", false).WithToolResult("reload", "", false).
		WithResponse([]dagger.LLMContentBlockInput{recomposeTool("added", "added")}).WithToolResult("added", "", false).
		WithResponse([]dagger.LLMContentBlockInput{recomposeTool("read", "readState")}).WithToolResult("read", "", false).
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "done"}})
	model := cannedRecordingModel(ctx, t, c, script)
	out, err := base.With(daggerShell(fmt.Sprintf(`
base=$(llm --model=%q | with-workspace --workspace $(current-workspace))
current-workspace | agents | compose --base $base | with-prompt "update and reload" | loop | transcript
`, model))).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "private counter: 1")
	require.Contains(t, out, "updated source; counter: 1")
	require.Regexp(t, `"memo"\s*:\s*"kept"`, out)
	require.NotContains(t, out, "already ran this turn")
}

func (LLMSuite) TestRecomposeLivePrivateRoster(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	workerScript := c.LLM().WithPrompt("opening").
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "worker opened"}}).
		WithPrompt("after reload").
		WithResponse([]dagger.LLMContentBlockInput{{Kind: dagger.LLMContentBlockKindText, Text: "same worker answered"}})
	workerModel := cannedRecordingModel(ctx, t, c, workerScript)
	roster := fmt.Sprintf(`
  let workers: Map[Agent!]! = [:]

  hire: Dagger.Swapper! @cache(policy: FunctionCachePolicy.Never) {
    let worker = llm(model: %q).spawn(name: "helper")
    worker.send("opening").response
    currentNode.{{... on Dagger.Swapper!}}.withWorker(worker)
  }

  withWorker(worker: Agent!): Swapper! {
    self.workers = workers.with("helper", worker)
    self
  }

  let member: Agent! {
    let worker = workers["helper"]
    if (worker == null) { raise "lost private roster" }
    worker
  }

  workerHandle: String! { "worker handle: " + member.handle }

  askWorker: String! @cache(policy: FunctionCachePolicy.Never) {
    member.send("after reload").response
  }

  harvest: String! @cache(policy: FunctionCachePolicy.Never) {
    "harvested: " + member.snapshot.lastReply
  }
`, workerModel)
	initial := fmt.Sprintf(recomposeSource, roster)
	updated := strings.ReplaceAll(initial, "Read the original private state.", "Read the updated private state.")
	fixture := recomposeFixture(c, initial)
	script := recomposeRecordingTurn(c.LLM(), "hire once", "hire", "workerHandle")
	script = recomposeRecordingTurn(script, "use roster", "workerHandle", "askWorker", "harvest")
	model := cannedRecordingModel(ctx, t, c, script)
	ws := fixture.AsWorkspace()
	llm := ws.Agents().Compose(dagger.AgentMiddlewareGroupComposeOpts{Base: c.LLM(dagger.LLMOpts{Model: model}).WithWorkspace(ws)}).
		WithPrompt("hire once").Loop()
	before, err := llm.Transcript(ctx)
	require.NoError(t, err)
	handlePattern := regexp.MustCompile(`worker handle: ([a-zA-Z0-9_-]+)`)
	beforeHandles := handlePattern.FindAllStringSubmatch(before, -1)
	require.Len(t, beforeHandles, 1)
	llm, err = recomposeLLM(ctx, c, ws.WithNewFile(recomposeModulePath, updated), llm)
	require.NoError(t, err)
	after, err := llm.WithPrompt("use roster").Loop().Transcript(ctx)
	require.NoError(t, err)
	afterHandles := handlePattern.FindAllStringSubmatch(after, -1)
	require.Len(t, afterHandles, 2)
	require.Equal(t, beforeHandles[0][1], afterHandles[1][1], "reload must not recreate the worker")
	require.Contains(t, after, "same worker answered")
	require.Contains(t, after, "harvested: same worker answered")
	require.NotContains(t, after, "lost private roster")
}

// Read stored system-message blocks, not Transcript: text shared with a user
// message or a tool result must not accidentally satisfy prompt assertions.
func recomposeSystemPrompts(ctx context.Context, t *testctx.T, c *dagger.Client, llm *dagger.LLM) []string {
	t.Helper()
	id, err := llm.ID(ctx)
	require.NoError(t, err)
	var res struct {
		Node struct {
			Messages []struct {
				Role    string
				Content []struct{ Text string }
			}
		}
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on LLM { messages { role content { text } } } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &res}))
	var prompts []string
	for _, message := range res.Node.Messages {
		if message.Role == "SYSTEM" {
			for _, block := range message.Content {
				prompts = append(prompts, block.Text)
			}
		}
	}
	return prompts
}

func (LLMSuite) TestRecomposeOwnedPrompts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const shared = "Caller and module deliberately use identical prompt text."
	const replacement = "The edited swapper prompt."
	const callerTail = "Caller prompt appended after composition."
	source := strings.ReplaceAll(fmt.Sprintf(recomposeSource, ""),
		"base.withTools(currentNode)", fmt.Sprintf("base.withSystemPrompt(%q).withTools(currentNode)", shared))
	fixture := recomposeFixture(c, source).
		WithNewFile("dagger.toml", "[modules.swapper]\nsource = \".dagger/modules/swapper\"\n[modules.other]\nsource = \"modules/other\"\n").
		WithNewFile("modules/other/dagger.json", `{"name":"other","engineVersion":"v1.0.0-0","sdk":"dang"}`)
	const otherSource = `
type Other {
  agent(base: LLM!): LLM! @agent {
    base.withSystemPrompt("The unrelated middleware prompt.").withTools(currentNode)
  }
  oldOtherTool: String! { "other original tool" }
}
`
	fixture = fixture.WithNewFile("modules/other/main.dang", otherSource)
	ws := fixture.AsWorkspace()
	llm := ws.Agents().Compose(dagger.AgentMiddlewareGroupComposeOpts{
		Base: c.LLM().WithWorkspace(ws).WithSystemPrompt(shared),
	}).WithSystemPrompt(callerTail)
	require.ElementsMatch(t, []string{shared, shared, callerTail, "The unrelated middleware prompt."},
		recomposeSystemPrompts(ctx, t, c, llm))

	// Record against the actual composed prompts so the replay model checks
	// exactly the history it will receive before the source edit.
	model := cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(llm, "mutate owned state", "advance", "readState"))
	llm = llm.WithModel(model).WithPrompt("mutate owned state").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 1")

	// Both modules change on disk, but only the selected owner's old prompt
	// and tools may be replaced. Identical caller text is not ownership.
	updated := strings.ReplaceAll(source, shared, replacement)
	updated = strings.ReplaceAll(updated, "Read the original private state.", "Read the selected owner's updated state.")
	changedOther := strings.ReplaceAll(strings.ReplaceAll(otherSource,
		"The unrelated middleware prompt.", "Unselected edit must not be applied."), "oldOtherTool", "newOtherTool")
	ws = ws.WithNewFile(recomposeModulePath, updated).
		WithNewFile("modules/other/main.dang", changedOther)
	llm, err = recomposeLLM(ctx, c, ws, llm, "swapper")
	require.NoError(t, err)
	expected := []string{shared, replacement, callerTail, "The unrelated middleware prompt."}
	require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, c, llm))
	tools, err := llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "Read the selected owner's updated state.")
	require.Contains(t, tools, "## oldOtherTool\n")
	require.NotContains(t, tools, "## newOtherTool\n")

	// Ownership must survive a new same-type state result too, not just the
	// first composition. This recording now uses the refreshed system blocks.
	model = cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(llm, "mutate reloaded state", "advance", "readState"))
	llm = llm.WithModel(model).WithPrompt("mutate reloaded state").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 2")
	portable, err := llm.PortableID(ctx)
	require.NoError(t, err)
	target := connect(ctx, t)
	llm = dagger.Ref[*dagger.LLM](target, portable)
	require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, target, llm))
	for range 2 {
		llm, err = recomposeLLM(ctx, target, llm.Workspace(), llm, "swapper")
		require.NoError(t, err)
		require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, target, llm))
		tools, err = llm.Tools(ctx)
		require.NoError(t, err)
		require.Contains(t, tools, "## oldOtherTool\n")
		require.NotContains(t, tools, "## newOtherTool\n")
	}
}

func (LLMSuite) TestRecomposePreservesManualContributions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const prompt = "An unowned prompt that also happens to be the module prompt."
	source := strings.ReplaceAll(fmt.Sprintf(recomposeSource, ""),
		"base.withTools(currentNode)", fmt.Sprintf("base.withSystemPrompt(%q).withTools(currentNode)", prompt))
	fixture := recomposeFixture(c, source)
	ws := fixture.AsWorkspace()
	file := c.Directory().WithNewFile("marker.txt", "manual file contents survived reload").File("marker.txt")
	manual := c.LLM().WithWorkspace(ws).WithSystemPrompt(prompt).WithTools(file)
	// Ordinary withTools does not retroactively claim a caller's prompts.
	require.Equal(t, []string{prompt}, recomposeSystemPrompts(ctx, t, c, manual))
	tools, err := manual.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## contents\n")

	// Explicit compose is still append-only, not a synonym for recompose.
	composed := ws.Agents().Compose(dagger.AgentMiddlewareGroupComposeOpts{Base: manual})
	require.ElementsMatch(t, []string{prompt, prompt}, recomposeSystemPrompts(ctx, t, c, composed))
	composed = ws.Agents().Compose(dagger.AgentMiddlewareGroupComposeOpts{Base: composed})
	require.ElementsMatch(t, []string{prompt, prompt, prompt}, recomposeSystemPrompts(ctx, t, c, composed))

	const replacement = "Replacement owned prompt, never the caller's."
	ws = ws.WithNewFile(recomposeModulePath, strings.ReplaceAll(source, prompt, replacement))
	for _, seed := range []*dagger.LLM{manual, composed} {
		// The legacy/manual seed has no ownership metadata to infer. Preserve
		// its prompt and capability while installing the new owned middleware.
		llm, err := recomposeLLM(ctx, c, ws, seed)
		require.NoError(t, err)
		for range 2 {
			require.ElementsMatch(t, []string{prompt, replacement}, recomposeSystemPrompts(ctx, t, c, llm))
			tools, err := llm.Tools(ctx)
			require.NoError(t, err)
			require.Contains(t, tools, "## contents\n")
			require.Contains(t, tools, "## readState\n")
			llm, err = recomposeLLM(ctx, c, ws, llm)
			require.NoError(t, err)
		}
		model := cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(llm, "read manual capability", "contents"))
		transcript, err := llm.WithModel(model).WithPrompt("read manual capability").Loop().Transcript(ctx)
		require.NoError(t, err)
		require.Contains(t, transcript, "manual file contents survived reload")
	}
}
