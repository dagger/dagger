package core

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"dagger.io/dagger/core"

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
    let ws = llm.workspace.withUserConfig
    llm.withWorkspace(ws).compose(expertise: llm.expertise.{{ id, artifact.{{ uri }} }}.map { e =>
      let found = ws.artifacts.filterUri(uri: e.artifact.uri).asExpertise.{{ id }}.map { f => f.id }
      node(id: found[0] ?? e.id).{{ ... on Expertise! }}
    })
  }
%s
}
`

func recomposeFixture(c *dagger.Client, source string) *core.Directory {
	return core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.swapper]\nsource = \".dagger/modules/swapper\"\n").
		WithNewFile(".dagger/modules/swapper/dagger.json", `{"name":"swapper","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile(recomposeModulePath, source)
}

// Entries' Agent arguments receive the conversation's owning agent: spawn
// composes a conversation it owns, and later compositions of that agent's
// conversations keep it. Use the live schema so these regressions do not
// depend on SDK regeneration.

// discoverExpertise converts ws's nominated (@agent) entries, optionally
// limited to include patterns.
func discoverExpertise(ctx context.Context, c *dagger.Client, ws *core.Workspace, include ...string) ([]core.ID, error) {
	wsID, err := ws.ID(ctx)
	if err != nil {
		return nil, err
	}
	var selected struct {
		Node struct {
			Artifacts struct {
				FilterTypes struct{ AsExpertise []struct{ ID core.ID } }
			}
		}
	}
	err = c.Do(ctx, &dagger.Request{
		Query: `query($workspace: ID!, $include: [String!]) {
   node(id: $workspace) { ... on Workspace { artifacts(include: $include) { filterTypes(types: ["Expertise"]) { asExpertise { id } } } } }
  }`,
		Variables: map[string]any{"workspace": wsID, "include": include},
	}, &dagger.Response{Data: &selected})
	if err != nil {
		return nil, err
	}
	ids := make([]core.ID, 0, len(selected.Node.Artifacts.FilterTypes.AsExpertise))
	for _, entry := range selected.Node.Artifacts.FilterTypes.AsExpertise {
		ids = append(ids, entry.ID)
	}
	return ids, nil
}

// spawnComposed spawns an agent from base, composed with ids, and returns
// the agent with its composed seed.
func spawnComposed(ctx context.Context, t *testctx.T, c *dagger.Client, base *core.LLM, ids []core.ID) (*agentHandle, *core.LLM, error) {
	t.Helper()
	baseID, err := base.ID(ctx)
	if err != nil {
		return nil, nil, err
	}
	if ids == nil {
		ids = []core.ID{}
	}
	var result struct{ Node struct{ Spawn string } }
	if err := c.Do(ctx, &dagger.Request{
		Query:     `query($base: ID!, $expertise: [ID!]!) { node(id: $base) { ... on LLM { spawn(expertise: $expertise) } } }`,
		Variables: map[string]any{"base": baseID, "expertise": ids},
	}, &dagger.Response{Data: &result}); err != nil {
		return nil, nil, err
	}
	agent := &agentHandle{c: c, agentID: result.Node.Spawn}
	out, err := agent.run(ctx, t, "snapshot { id }")
	if err != nil {
		return nil, nil, err
	}
	return agent, core.Ref[*core.LLM](core.NewQuery(c), core.ID(out.Get("snapshot.id").String())), nil
}

// composeRecomposeFixture spawns an agent composed with ws's nominated
// entries, from base or a fresh conversation bound to ws.
func composeRecomposeFixture(ctx context.Context, t *testctx.T, c *dagger.Client, ws *core.Workspace, base *core.LLM, include ...string) (*agentHandle, *core.LLM) {
	t.Helper()
	if base == nil {
		base = core.NewQuery(c).LLM().WithWorkspace(ws)
	}
	ids, err := discoverExpertise(ctx, c, ws, include...)
	require.NoError(t, err)
	agent, llm, err := spawnComposed(ctx, t, c, base, ids)
	require.NoError(t, err)
	return agent, llm
}

// composeInto runs LLM.compose: the entries are recorded, or refreshed in
// place when already recorded.
func composeInto(ctx context.Context, c *dagger.Client, conversation *core.LLM, ids []core.ID) (*core.LLM, error) {
	if ids == nil {
		ids = []core.ID{}
	}
	return llmComposition(ctx, c, conversation, `compose(expertise: $expertise)`, map[string]any{"expertise": ids})
}

// reloadConversation refreshes a conversation's record the way a reload tool
// does, as the GraphQL chain equivalent of
//
//	let ws = llm.workspace.withUserConfig
//	llm.withWorkspace(ws).compose(expertise: llm.expertise.{{ id, artifact.{{ uri }} }}.map { e =>
//	  let found = ws.artifacts.filterUri(uri: e.artifact.uri).asExpertise.{{ id }}.map { f => f.id }
//	  node(id: found[0] ?? e.id).{{ ... on Expertise! }}
//	})
//
// Each recorded entry is looked up again in the bound workspace by its URI,
// dimension keys included, falling back to the recorded entry where the
// workspace does not address it; a broken module there fails the lookup.
func reloadConversation(ctx context.Context, c *dagger.Client, conversation *core.LLM) (*core.LLM, error) {
	llmID, err := conversation.ID(ctx)
	if err != nil {
		return nil, err
	}
	var res struct {
		Node struct {
			Workspace struct {
				WithUserConfig struct{ ID core.ID }
			}
			Expertise []struct {
				ID       core.ID
				Artifact struct{ URI string }
			}
		}
	}
	if err := c.Do(ctx, &dagger.Request{
		Query:     `query($llm: ID!) { node(id: $llm) { ... on LLM { workspace { withUserConfig { id } } expertise { id artifact { uri } } } } }`,
		Variables: map[string]any{"llm": llmID},
	}, &dagger.Response{Data: &res}); err != nil {
		return nil, err
	}
	ws := core.Ref[*core.Workspace](core.NewQuery(c), res.Node.Workspace.WithUserConfig.ID)
	ids := make([]core.ID, 0, len(res.Node.Expertise))
	for _, entry := range res.Node.Expertise {
		found, err := uriExpertiseIDs(ctx, c, ws, entry.Artifact.URI)
		if err != nil {
			return nil, err
		}
		if len(found) > 0 {
			ids = append(ids, found[0])
		} else {
			ids = append(ids, entry.ID)
		}
	}
	return composeInto(ctx, c, conversation.WithWorkspace(ws), ids)
}

func llmComposition(ctx context.Context, c *dagger.Client, conversation *core.LLM, selection string, vars map[string]any) (*core.LLM, error) {
	llmID, err := conversation.ID(ctx)
	if err != nil {
		return nil, err
	}
	decls := "$llm: ID!"
	if vars == nil {
		vars = map[string]any{}
	} else {
		decls += ", $expertise: [ID!]!"
	}
	vars["llm"] = llmID
	var result struct {
		Node struct{ Result struct{ ID core.ID } }
	}
	if err := c.Do(ctx, &dagger.Request{
		Query:     fmt.Sprintf(`query(%s) { node(id: $llm) { ... on LLM { result: %s { id } } } }`, decls, selection),
		Variables: vars,
	}, &dagger.Response{Data: &result}); err != nil {
		return nil, err
	}
	return core.Ref[*core.LLM](core.NewQuery(c), result.Node.Result.ID), nil
}

// recomposeLLM binds ws, reloads base's record there, then composes the
// entries discovered in ws, refreshing recorded ones in place.
func recomposeLLM(ctx context.Context, t *testctx.T, c *dagger.Client, ws *core.Workspace, base *core.LLM, include ...string) (*core.LLM, error) {
	t.Helper()
	llm, err := refreshLLM(ctx, c, ws, base)
	if err != nil {
		return nil, err
	}
	ids, err := discoverExpertise(ctx, c, ws, include...)
	if err != nil {
		return nil, err
	}
	return composeInto(ctx, c, llm, ids)
}

// composeLLM binds ws and composes the entries discovered there, without
// refreshing the rest of the record.
func composeLLM(ctx context.Context, c *dagger.Client, ws *core.Workspace, base *core.LLM, include ...string) (*core.LLM, error) {
	ids, err := discoverExpertise(ctx, c, ws, include...)
	if err != nil {
		return nil, err
	}
	return composeInto(ctx, c, base.WithWorkspace(ws), ids)
}

// refreshLLM binds ws and reloads: every recorded entry is looked up again in
// ws, or reruns from its recorded source.
func refreshLLM(ctx context.Context, c *dagger.Client, ws *core.Workspace, base *core.LLM) (*core.LLM, error) {
	return reloadConversation(ctx, c, base.WithWorkspace(ws))
}

// composeURI binds ws and composes the entries a URI selects there.
func composeURI(ctx context.Context, c *dagger.Client, ws *core.Workspace, base *core.LLM, uri string) (*core.LLM, error) {
	ids, err := uriExpertiseIDs(ctx, c, ws, uri)
	if err != nil {
		return nil, err
	}
	return composeInto(ctx, c, base.WithWorkspace(ws), ids)
}

// spawnURI spawns an agent from base bound to ws, composed with the entries
// a URI selects there.
func spawnURI(ctx context.Context, t *testctx.T, c *dagger.Client, ws *core.Workspace, base *core.LLM, uri string) (*agentHandle, *core.LLM) {
	t.Helper()
	ids, err := uriExpertiseIDs(ctx, c, ws, uri)
	require.NoError(t, err)
	agent, llm, err := spawnComposed(ctx, t, c, base.WithWorkspace(ws), ids)
	require.NoError(t, err)
	return agent, llm
}

// restoreComposer restores agent's runtime in another client's session, under
// its handle and name, from a conversation recipe captured from it, and
// returns the restored agent with its seed: the conversation, owned by the
// restored agent. The owner must be present in a session for entries' Agent
// arguments to resolve there.
func restoreComposer(ctx context.Context, t *testctx.T, target *dagger.Client, agent *agentHandle, recipe core.ID) (*agentHandle, *core.LLM) {
	t.Helper()
	info := agent.mustRun(ctx, t, "handle name")
	restored, err := rehydrateAgent(ctx, target, string(recipe), info.Get("handle").String(), info.Get("name").String(), "IDLE", "")
	require.NoError(t, err)
	seed := restored.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()
	return restored, core.Ref[*core.LLM](core.NewQuery(target), core.ID(seed))
}

func uriExpertiseIDs(ctx context.Context, c *dagger.Client, ws *core.Workspace, uri string) ([]core.ID, error) {
	wsID, err := ws.ID(ctx)
	if err != nil {
		return nil, err
	}
	var res struct {
		Node struct {
			Artifacts struct {
				FilterURI struct{ AsExpertise []struct{ ID core.ID } }
			}
		}
	}
	err = c.Do(ctx, &dagger.Request{
		Query: `query($ws: ID!, $uri: String!) {
			node(id: $ws) { ... on Workspace { artifacts { filterUri(uri: $uri) { asExpertise { id } } } } }
		}`,
		Variables: map[string]any{"ws": wsID, "uri": uri},
	}, &dagger.Response{Data: &res})
	if err != nil {
		return nil, err
	}
	var ids []core.ID
	for _, entry := range res.Node.Artifacts.FilterURI.AsExpertise {
		ids = append(ids, entry.ID)
	}
	return ids, nil
}

type recordedExpertiseEntry struct {
	Artifact struct{ URI string }
}

func recordedExpertise(ctx context.Context, t *testctx.T, c *dagger.Client, llm *core.LLM) []recordedExpertiseEntry {
	t.Helper()
	id, err := llm.ID(ctx)
	require.NoError(t, err)
	var res struct {
		Node struct{ Expertise []recordedExpertiseEntry }
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on LLM { expertise { artifact { uri } } } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &res}))
	return res.Node.Expertise
}

// Refresh the composition record in ws with the reload lookup by URI,
// not old Expertise IDs or compose-all discovery (which would install the chief).
func recomposeRecordedExpertise(ctx context.Context, t *testctx.T, c *dagger.Client, ws *core.Workspace, llm *core.LLM) *core.LLM {
	t.Helper()
	result, err := refreshLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	return result
}

func recomposeTool(id, name string) core.LLMContentBlockInput {
	return core.LLMContentBlockInput{Kind: core.LLMContentBlockKindToolCall, CallID: id, ToolName: name}
}

func recomposeRecordingTurn(llm *core.LLM, prompt string, tools ...string) *core.LLM {
	llm = llm.WithPrompt(prompt)
	for i, tool := range tools {
		call := recomposeTool(fmt.Sprintf("%s_%d", prompt, i), tool)
		llm = llm.WithResponse([]core.LLMContentBlockInput{call}).WithToolResult(call.CallID, "", false)
	}
	return llm.WithResponse([]core.LLMContentBlockInput{{Kind: core.LLMContentBlockKindText, Text: prompt + " done"}})
}

func (LLMSuite) TestRecomposePrivateStateAndReplay(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
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
	script := recomposeRecordingTurn(core.NewQuery(c).LLM(), "mutate", "advance", "readState")
	script = recomposeRecordingTurn(script, "unchanged", "readState")
	script = recomposeRecordingTurn(script, "edited", "added", "advance", "added", "readState")
	script = recomposeRecordingTurn(script, "restored", "advance", "added", "readState")
	script = recomposeRecordingTurn(script, "installed", "added", "extraTool", "readState")
	model := cannedRecordingModel(ctx, t, c, script)
	mapPattern := regexp.MustCompile(`"memo"\s*:\s*"kept"`)
	agent, llm := composeRecomposeFixture(ctx, t, c, ws, core.NewQuery(c).LLM(core.LLMOpts{Model: model}).WithWorkspace(ws))
	llm = llm.WithPrompt("mutate").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 1")
	require.Regexp(t, `"memo"\s*:\s*"kept"`, transcript)

	llm, err = recomposeLLM(ctx, t, c, ws, llm)
	require.NoError(t, err)
	llm = llm.WithPrompt("unchanged").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(transcript, "private counter: 1"))
	require.Len(t, mapPattern.FindAllString(transcript, -1), 2)

	// Edit the existing workspace overlay, as the editor does: rebuilding a
	// Directory-backed workspace would give the module a different origin.
	// The reload lookup finds the recorded entry again there.
	ws = ws.WithNewFile(recomposeModulePath, updated)
	llm, err = refreshLLM(ctx, c, ws, llm)
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
		llm, err = refreshLLM(ctx, c, ws, llm)
		require.NoError(t, err)
	}
	portable, err := sink.captureLLMRecipe(ctx, t, c, llm)
	require.NoError(t, err)
	target := connect(ctx, t)
	_, restored := restoreComposer(ctx, t, target, agent, portable)
	llm = restored.WithPrompt("restored").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "new field default; updated counter: 3")
	require.Contains(t, transcript, "private counter: 3")
	require.Len(t, mapPattern.FindAllString(transcript, -1), 4)
	require.NotContains(t, transcript, "is not available")

	// Adding expertise is the install path: initialize only the new module,
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
	llm, err = recomposeLLM(ctx, t, target, installed, llm)
	require.NoError(t, err)
	transcript, err = llm.WithPrompt("installed").Loop().Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "installed default")
	require.Equal(t, 2, strings.Count(transcript, "new field default; updated counter: 3"))
	require.Equal(t, 2, strings.Count(transcript, "private counter: 3"))
	require.Len(t, mapPattern.FindAllString(transcript, -1), 5)
}

func (LLMSuite) TestRecomposeRemoteToLocalStateAndReplay(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	const remotePrompt = "The remote swapper prompt."
	const localPrompt = "The locally installed swapper prompt."
	initial := strings.ReplaceAll(fmt.Sprintf(recomposeSource, `
  remoteOnly: String! { "remote implementation" }
`), "base.withTools(currentNode)", fmt.Sprintf("base.withSystemPrompt(%q).withTools(currentNode)", remotePrompt))
	updated := strings.ReplaceAll(fmt.Sprintf(recomposeSource, `
  let addedDefault: String! = "local field default"

  """A tool from the local installation."""
  added: String! { addedDefault + "; local counter: " + toString(state) }
`), "base.withTools(currentNode)", fmt.Sprintf("base.withSystemPrompt(%q).withTools(currentNode)", localPrompt))
	updated = strings.ReplaceAll(updated, "Read the original private state.", "Read the locally installed private state.")

	// Serve a real remote module without depending on an external repository.
	// Replacing its installation must not make source identity part of state
	// compatibility or ownership of the expertise's tools and prompts.
	remoteRef := workspaceSelectionRemoteRef(ctx, t, c, recomposeFixture(c, initial).Directory(".dagger/modules/swapper"))
	ws := core.NewQuery(c).Directory().WithNewFile("dagger.toml", fmt.Sprintf("[modules.swapper]\nsource = %q\n", remoteRef)).AsWorkspace()
	_, llm := composeRecomposeFixture(ctx, t, c, ws, core.NewQuery(c).LLM().WithWorkspace(ws).WithSystemPrompt(remotePrompt))
	require.ElementsMatch(t, []string{remotePrompt, remotePrompt}, recomposeSystemPrompts(ctx, t, c, llm))
	model := cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(llm, "mutate remote", "advance", "readState"))
	llm = llm.WithModel(model).WithPrompt("mutate remote").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 1")
	mapPattern := regexp.MustCompile(`"memo"\s*:\s*"kept"`)
	require.Len(t, mapPattern.FindAllString(transcript, -1), 1)

	// Keep the installed name and object type, but switch from a git source
	// to local code. This is not just an edit within the original origin.
	ws = ws.WithNewFile("dagger.toml", "[modules.swapper]\nsource = \".dagger/modules/swapper\"\n").
		WithNewFile(".dagger/modules/swapper/dagger.json", `{"name":"swapper","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile(recomposeModulePath, updated)
	llm, err = recomposeLLM(ctx, t, c, ws, llm, "swapper")
	require.NoError(t, err)
	// The caller's identical prompt survives, while the remote-owned prompt
	// is replaced rather than left behind or duplicated.
	expectedPrompts := []string{remotePrompt, localPrompt}
	require.ElementsMatch(t, expectedPrompts, recomposeSystemPrompts(ctx, t, c, llm))
	tools, err := llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## added\n")
	require.Contains(t, tools, "A tool from the local installation.")
	require.Contains(t, tools, "Read the locally installed private state.")
	require.NotContains(t, tools, "Read the original private state.")
	require.NotContains(t, tools, "## remoteOnly\n")

	model = cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(llm, "use local", "added", "advance", "added", "readState"))
	llm = llm.WithModel(model).WithPrompt("use local").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "local field default; local counter: 1")
	require.Contains(t, transcript, "local field default; local counter: 2")
	require.Contains(t, transcript, "private counter: 2")
	require.Len(t, mapPattern.FindAllString(transcript, -1), 2)

	// A same-type state return and a repeated reload must keep local code
	// and ownership, including when the portable recipe is loaded elsewhere.
	llm, err = recomposeLLM(ctx, t, c, ws, llm, "swapper")
	require.NoError(t, err)
	require.ElementsMatch(t, expectedPrompts, recomposeSystemPrompts(ctx, t, c, llm))
	model = cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(llm, "replay local", "advance", "added", "readState"))
	portable, err := sink.captureLLMRecipe(ctx, t, c, llm.WithModel(model))
	require.NoError(t, err)
	target := connect(ctx, t)
	llm = core.Ref[*core.LLM](core.NewQuery(target), portable)
	require.ElementsMatch(t, expectedPrompts, recomposeSystemPrompts(ctx, t, target, llm))
	transcript, err = llm.WithPrompt("replay local").Loop().Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "local field default; local counter: 3")
	require.Contains(t, transcript, "private counter: 3")
	require.Len(t, mapPattern.FindAllString(transcript, -1), 3)
	require.NotContains(t, transcript, "is not available")
}

// Bumping a remote module's pin in a live workspace's pending dagger.lock and
// reloading must load the newly pinned commit, not the session's served one.
func (LLMSuite) TestRecomposeOverlayLockRepinsRemoteModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	initial := fmt.Sprintf(recomposeSource, "")
	bumped := strings.ReplaceAll(initial, "Read the original private state.", "Read the bumped private state.")

	// A remote module with two commits on main, without depending on an
	// external repository: A, then B at the branch head.
	repo := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/repo", recomposeFixture(c, initial).Directory(".dagger/modules/swapper")).
		WithWorkdir("/repo").
		WithExec([]string{"sh", "-c", `set -e
git init -q -b main
git -c user.email=root@localhost -c user.name=Test add -A
git -c user.email=root@localhost -c user.name=Test commit -q -m A`}).
		WithNewFile("/repo/main.dang", bumped).
		WithExec([]string{"sh", "-c", `git -c user.email=root@localhost -c user.name=Test commit -q -am B`})
	commits, err := repo.WithExec([]string{"git", "rev-parse", "HEAD~1", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	shas := strings.Fields(commits)
	require.Len(t, shas, 2)
	commitA, commitB := shas[0], shas[1]
	remoteRef := workspaceSelectionRemoteRef(ctx, t, c, repo.Directory("/repo"))

	config := fmt.Sprintf("[modules.swapper]\nsource = %q\n", remoteRef)
	// A live, host-backed workspace: overlays on it re-resolve only what they
	// touch, on top of the modules the session serves from disk.
	base := workspaceBase(t, c).WithNewFile("dagger.toml", config)
	const selectCommit = `artifacts(include: ["swapper"]) { filterTypes(types: ["Expertise"]) { asExpertise { originalModule { source { commit } } } } }`
	commitOf := func(ctx context.Context, t *testctx.T, ctr *core.Container, query string, args ...any) string {
		t.Helper()
		out, err := ctr.With(daggerQuery(query, args...)).Stdout(ctx)
		require.NoError(t, err)
		switch {
		case strings.Contains(out, commitA) && !strings.Contains(out, commitB):
			return commitA
		case strings.Contains(out, commitB) && !strings.Contains(out, commitA):
			return commitB
		}
		t.Fatalf("expected exactly one of %s and %s: %s", commitA, commitB, out)
		return ""
	}

	// Let the engine write the lock for the branch head (B), then pin A on
	// disk: the served module follows the on-disk pin.
	locked := base.With(daggerQuery(`{ currentWorkspace { %s } }`, selectCommit))
	lockB, err := locked.File("dagger.lock").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, lockB, commitB)
	lockA := strings.ReplaceAll(lockB, commitB, commitA)
	base = base.WithNewFile("dagger.lock", lockA)
	require.Equal(t, commitA, commitOf(ctx, t, base, `{ currentWorkspace { %s } }`, selectCommit))

	t.Run("lock edit", func(ctx context.Context, t *testctx.T) {
		require.Equal(t, commitB, commitOf(ctx, t, base,
			`{ currentWorkspace { withNewFile(path: "dagger.lock", contents: %q) { %s } } }`, lockB, selectCommit))
	})

	t.Run("config and lock edit", func(ctx context.Context, t *testctx.T) {
		require.Equal(t, commitB, commitOf(ctx, t, base,
			`{ currentWorkspace { withNewFile(path: "dagger.toml", contents: %q) { withNewFile(path: "dagger.lock", contents: %q) { %s } } } }`,
			config+"# touched\n", lockB, selectCommit))
	})

	t.Run("config edit keeps the on-disk pin", func(ctx context.Context, t *testctx.T) {
		require.Equal(t, commitA, commitOf(ctx, t, base,
			`{ currentWorkspace { withNewFile(path: "dagger.toml", contents: %q) { %s } } }`,
			config+"# touched\n", selectCommit))
	})
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
			script := recomposeRecordingTurn(core.NewQuery(c).LLM(), "mutate", "advance")
			script = recomposeRecordingTurn(script, "still usable", "advance", "readState")
			model := cannedRecordingModel(ctx, t, c, script)
			_, llm := composeRecomposeFixture(ctx, t, c, ws, core.NewQuery(c).LLM(core.LLMOpts{Model: model}).WithWorkspace(ws))
			llm = llm.WithPrompt("mutate").Loop()
			_, err := llm.Sync(ctx)
			require.NoError(t, err)
			// A recorded module that fails to load is an error, never a silent
			// fallback to its previously recorded source.
			_, err = refreshLLM(ctx, c, ws.WithNewFile(recomposeModulePath, tc.source), llm)
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

func (LLMSuite) TestRecomposeRefreshesConstruction(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// Configuration is constructed, not tool state: a reload re-resolves it
	// (settings, defaults, session-bound values such as secrets) while state
	// that tool calls recorded carries over. The module code is unchanged, so
	// this also covers the same-implementation path.
	const source = `
type Swapper {
  let state: Int! = 0
  let label: String! = ""

  new(label: String! = "unset") {
    self.label = label
    self
  }

  agent(base: LLM!): LLM! @agent {
    base.withTools(currentNode)
  }

  advance: Swapper! {
    state += 1
    self
  }

  readConfig: String! {
    "label: " + label + "; counter: " + toString(state)
  }
}
`
	config := func(label string) string {
		return "[modules.swapper]\nsource = \".dagger/modules/swapper\"\n\n[modules.swapper.settings]\nlabel = \"" + label + "\"\n"
	}
	ws := recomposeFixture(c, source).WithNewFile("dagger.toml", config("first")).AsWorkspace()
	script := recomposeRecordingTurn(core.NewQuery(c).LLM(), "mutate", "advance", "readConfig")
	script = recomposeRecordingTurn(script, "unchanged", "readConfig")
	script = recomposeRecordingTurn(script, "reconfigured", "readConfig", "advance", "readConfig")
	model := cannedRecordingModel(ctx, t, c, script)
	llm := composeRecomposeFixture(ctx, t, c, ws, core.NewQuery(c).LLM(core.LLMOpts{Model: model}).WithWorkspace(ws)).
		WithPrompt("mutate").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "label: first; counter: 1")

	llm, err = recomposeLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	llm = llm.WithPrompt("unchanged").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(transcript, "label: first; counter: 1"))

	ws = ws.WithNewFile("dagger.toml", config("second"))
	llm, err = recomposeLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	transcript, err = llm.WithPrompt("reconfigured").Loop().Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "label: second; counter: 1", "the edited setting takes effect, the counter carries over")
	require.Contains(t, transcript, "label: second; counter: 2")
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
	script := recomposeRecordingTurn(core.NewQuery(c).LLM(), "mutate", "advance", "readState")
	script = recomposeRecordingTurn(script, "reset", "readState", "advance")
	script = recomposeRecordingTurn(script, "carried", "added", "readState")
	model := cannedRecordingModel(ctx, t, c, script)
	_, llm := composeRecomposeFixture(ctx, t, c, ws, core.NewQuery(c).LLM(core.LLMOpts{Model: model}).WithWorkspace(ws))
	llm = llm.WithPrompt("mutate").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 1")
	require.Regexp(t, `"memo"\s*:\s*"kept"`, transcript)

	// Changing the withTools version from its default of 0 to 2 discards the
	// old state rather than overlaying it onto the retyped map.
	ws = ws.WithNewFile(recomposeModulePath, versioned)
	llm, err = recomposeLLM(ctx, t, c, ws, llm)
	require.NoError(t, err)
	llm = llm.WithPrompt("reset").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 0")
	require.Regexp(t, `"seed"\s*:\s*42`, transcript)
	require.Len(t, regexp.MustCompile(`"memo"\s*:\s*"kept"`).FindAllString(transcript, -1), 1, "the old memo is gone after the reset")

	// An edit at the same version carries the (now version 2) state over.
	ws = ws.WithNewFile(recomposeModulePath, extended)
	llm, err = recomposeLLM(ctx, t, c, ws, llm)
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

func recomposeGoFixture(c *dagger.Client, source string) *core.Directory {
	return core.NewQuery(c).Directory().
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
	script := recomposeRecordingTurn(core.NewQuery(c).LLM(), "mutate", "advance", "readState")
	script = recomposeRecordingTurn(script, "edited", "added", "advance", "readState")
	script = recomposeRecordingTurn(script, "reset", "readState")
	model := cannedRecordingModel(ctx, t, c, script)
	_, llm := composeRecomposeFixture(ctx, t, c, ws, core.NewQuery(c).LLM(core.LLMOpts{Model: model}).WithWorkspace(ws))
	llm = llm.WithPrompt("mutate").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 1")
	require.Contains(t, transcript, "memo=kept")

	// A new revision with an added private field and method: existing state
	// carries over, the new field takes the new constructor's default.
	ws = ws.WithNewFile(recomposeGoModulePath, extended)
	llm, err = recomposeLLM(ctx, t, c, ws, llm)
	require.NoError(t, err)
	llm = llm.WithPrompt("edited").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "new field default; updated counter: 1")
	require.Contains(t, transcript, "private counter: 2")
	require.Equal(t, 2, strings.Count(transcript, "memo=kept"))

	// Changing the withTools version resets the object to the new defaults.
	ws = ws.WithNewFile(recomposeGoModulePath, versioned)
	llm, err = refreshLLM(ctx, c, ws, llm)
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
	ws := recomposeFixture(c, initial).WithNewFile("next-source.txt", updated).AsWorkspace()
	// Continuations run in their written position, on the state the calls
	// before them produced: reload must see both the source edit and the
	// same-type receiver returned by the calls emitted before it. Reload runs
	// in an agent loop, on the conversation that agent owns.
	script := core.NewQuery(c).LLM().WithPrompt("update and reload").WithResponse([]core.LLMContentBlockInput{
		recomposeTool("advance", "advance"), recomposeTool("edit", "updateSource"), recomposeTool("reload", "reload"),
	}).WithToolResult("advance", "", false).WithToolResult("edit", "", false).WithToolResult("reload", "", false).
		WithResponse([]core.LLMContentBlockInput{recomposeTool("added", "added")}).WithToolResult("added", "", false).
		WithResponse([]core.LLMContentBlockInput{recomposeTool("read", "readState")}).WithToolResult("read", "", false).
		WithResponse([]core.LLMContentBlockInput{{Kind: core.LLMContentBlockKindText, Text: "done"}})
	model := cannedRecordingModel(ctx, t, c, script)
	ids, err := discoverExpertise(ctx, c, ws)
	require.NoError(t, err)
	agent := spawnWithExpertise(ctx, t, c, core.NewQuery(c).LLM(core.LLMOpts{Model: model}).WithWorkspace(ws), "same-batch", ids)
	_, _, err = agent.sendAndWait(ctx, t, "update and reload")
	require.NoError(t, err)
	out, _ := agent.snapshot(ctx, t)
	require.Contains(t, out, "private counter: 1")
	require.Contains(t, out, "updated source; counter: 1")
	require.Regexp(t, `"memo"\s*:\s*"kept"`, out)
	require.NotContains(t, out, "already ran this turn")
}

func (LLMSuite) TestRecomposeLivePrivateRoster(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	workerScript := core.NewQuery(c).LLM().WithPrompt("opening").
		WithResponse([]core.LLMContentBlockInput{{Kind: core.LLMContentBlockKindText, Text: "worker opened"}}).
		WithPrompt("after reload").
		WithResponse([]core.LLMContentBlockInput{{Kind: core.LLMContentBlockKindText, Text: "same worker answered"}})
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
	script := recomposeRecordingTurn(core.NewQuery(c).LLM(), "hire once", "hire", "workerHandle")
	script = recomposeRecordingTurn(script, "use roster", "workerHandle", "askWorker", "harvest")
	model := cannedRecordingModel(ctx, t, c, script)
	ws := fixture.AsWorkspace()
	_, llm := composeRecomposeFixture(ctx, t, c, ws, core.NewQuery(c).LLM(core.LLMOpts{Model: model}).WithWorkspace(ws))
	llm = llm.WithPrompt("hire once").Loop()
	before, err := llm.Transcript(ctx)
	require.NoError(t, err)
	handlePattern := regexp.MustCompile(`worker handle: ([a-zA-Z0-9_-]+)`)
	beforeHandles := handlePattern.FindAllStringSubmatch(before, -1)
	require.Len(t, beforeHandles, 1)
	llm, err = refreshLLM(ctx, c, ws.WithNewFile(recomposeModulePath, updated), llm)
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
func recomposeSystemPrompts(ctx context.Context, t *testctx.T, c *dagger.Client, llm *core.LLM) []string {
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
	c, sink := connectWithTrace(ctx, t)
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
    base.withSystemPrompt("The unrelated expertise prompt.").withTools(currentNode)
  }
  oldOtherTool: String! { "other original tool" }
}
`
	fixture = fixture.WithNewFile("modules/other/main.dang", otherSource)
	ws := fixture.AsWorkspace()
	agent, llm := composeRecomposeFixture(ctx, t, c, ws, core.NewQuery(c).LLM().WithWorkspace(ws).WithSystemPrompt(shared))
	llm = llm.WithSystemPrompt(callerTail)
	require.ElementsMatch(t, []string{shared, shared, callerTail, "The unrelated expertise prompt."},
		recomposeSystemPrompts(ctx, t, c, llm))

	// Record against the actual composed prompts so the replay model checks
	// exactly the history it will receive before the source edit.
	model := cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(llm, "mutate owned state", "advance", "readState"))
	llm = llm.WithModel(model).WithPrompt("mutate owned state").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 1")

	// Both modules change on disk. Reloading refreshes every recorded
	// entry, replacing only each owner's old prompt and tools: identical
	// caller text is not ownership.
	updated := strings.ReplaceAll(source, shared, replacement)
	updated = strings.ReplaceAll(updated, "Read the original private state.", "Read the owner's updated state.")
	changedOther := strings.ReplaceAll(strings.ReplaceAll(otherSource,
		"The unrelated expertise prompt.", "The edited unrelated prompt."), "oldOtherTool", "newOtherTool")
	ws = ws.WithNewFile(recomposeModulePath, updated).
		WithNewFile("modules/other/main.dang", changedOther)
	llm, err = refreshLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	expected := []string{shared, replacement, callerTail, "The edited unrelated prompt."}
	require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, c, llm))
	tools, err := llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "Read the owner's updated state.")
	require.Contains(t, tools, "## newOtherTool\n")
	require.NotContains(t, tools, "## oldOtherTool\n")

	// Ownership must survive a new same-type state result too, not just the
	// first composition. This recording now uses the refreshed system blocks.
	model = cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(llm, "mutate reloaded state", "advance", "readState"))
	llm = llm.WithModel(model).WithPrompt("mutate reloaded state").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 2")
	portable, err := sink.captureLLMRecipe(ctx, t, c, llm)
	require.NoError(t, err)
	target := connect(ctx, t)
	_, llm = restoreComposer(ctx, t, target, agent, portable)
	require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, target, llm))
	for range 2 {
		llm, err = recomposeLLM(ctx, t, target, llm.Workspace(), llm, "swapper")
		require.NoError(t, err)
		require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, target, llm))
		tools, err = llm.Tools(ctx)
		require.NoError(t, err)
		require.Contains(t, tools, "## newOtherTool\n")
		require.NotContains(t, tools, "## oldOtherTool\n")
	}
}

func (LLMSuite) TestRecomposeNestedCompositionPrompts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	fixture := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.outer]\nsource = \"outer\"\n[modules.inner]\nsource = \"inner\"\n").
		WithNewFile("outer/dagger.json", `{"name":"outer","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("outer/main.dang", `
type Outer {
  agent(base: LLM!, ws: Workspace!): LLM! @agent {
    base.withSystemPrompt("outer prompt").compose(expertise: ws.artifacts(include: ["inner"]).filterTypes(types: ["Expertise"]).asExpertise.{{ id }}.map { node(id: _.id).{{ ... on Expertise! }} })
  }
}
`).
		WithNewFile("inner/dagger.json", `{"name":"inner","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("inner/main.dang", `
type Inner {
  agent(base: LLM!): LLM! @agent { base.withSystemPrompt("inner prompt") }
}
`)
	ws := fixture.AsWorkspace()
	_, llm := composeRecomposeFixture(ctx, t, c, ws, nil, "outer")
	// The nested entry is recorded on its own, after the entry composing it.
	entries := recordedExpertise(ctx, t, c, llm)
	require.Len(t, entries, 2)
	require.Contains(t, entries[0].Artifact.URI, "outer/agent")
	require.Contains(t, entries[1].Artifact.URI, "inner/agent")
	expected := []string{"outer prompt", "inner prompt"}
	require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, c, llm))
	for range 2 {
		var err error
		llm, err = recomposeLLM(ctx, t, c, ws, llm, "outer")
		require.NoError(t, err)
		// Refreshing Outer reruns its nested compose, which finds Inner
		// recorded and leaves it alone rather than appending it again.
		require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, c, llm),
			"nested compose of a recorded entry is not repeated")
		require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
	}
	llm, err := composeLLM(ctx, c, ws, llm, "inner")
	require.NoError(t, err)
	require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, c, llm),
		"composing the recorded Inner entry refreshes it in place")
	require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
}

func (LLMSuite) TestRecomposePreservesManualContributions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const prompt = "An unowned prompt that also happens to be the module prompt."
	source := strings.ReplaceAll(fmt.Sprintf(recomposeSource, ""),
		"base.withTools(currentNode)", fmt.Sprintf("base.withSystemPrompt(%q).withTools(currentNode)", prompt))
	fixture := recomposeFixture(c, source)
	ws := fixture.AsWorkspace()
	file := core.NewQuery(c).Directory().WithNewFile("marker.txt", "manual file contents survived reload").File("marker.txt")
	manual := core.NewQuery(c).LLM().WithWorkspace(ws).WithSystemPrompt(prompt).WithTools(file)
	// Ordinary withTools does not retroactively claim a caller's prompts.
	require.Equal(t, []string{prompt}, recomposeSystemPrompts(ctx, t, c, manual))
	tools, err := manual.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## contents\n")

	// Each entry is recorded once; composing an identical entry again
	// refreshes it in place rather than duplicating its contributions.
	_, composed := composeRecomposeFixture(ctx, t, c, ws, manual)
	require.ElementsMatch(t, []string{prompt, prompt}, recomposeSystemPrompts(ctx, t, c, composed))
	again, err := composeLLM(ctx, c, ws, composed)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{prompt, prompt}, recomposeSystemPrompts(ctx, t, c, again))
	require.Equal(t, recordedExpertise(ctx, t, c, composed), recordedExpertise(ctx, t, c, again))

	const replacement = "Replacement owned prompt, never the caller's."
	ws = ws.WithNewFile(recomposeModulePath, strings.ReplaceAll(source, prompt, replacement))
	for _, seed := range []*core.LLM{manual, composed} {
		// The legacy/manual seed has no ownership metadata to infer. Preserve
		// its prompt and capability while installing the new owned expertise.
		// No entry needs an Agent, so the unowned conversation composes too.
		llm, err := recomposeLLM(ctx, t, c, ws, seed)
		require.NoError(t, err)
		for range 2 {
			require.ElementsMatch(t, []string{prompt, replacement}, recomposeSystemPrompts(ctx, t, c, llm))
			tools, err := llm.Tools(ctx)
			require.NoError(t, err)
			require.Contains(t, tools, "## contents\n")
			require.Contains(t, tools, "## readState\n")
			llm, err = recomposeLLM(ctx, t, c, ws, llm)
			require.NoError(t, err)
		}
		model := cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(llm, "read manual capability", "contents"))
		transcript, err := llm.WithModel(model).WithPrompt("read manual capability").Loop().Transcript(ctx)
		require.NoError(t, err)
		require.Contains(t, transcript, "manual file contents survived reload")
	}
}

func (LLMSuite) TestRecomposeNestedToolState(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	const outerSource = `
type Outer {
  agent(base: LLM!, ws: Workspace!): LLM! @agent {
    base.compose(expertise: ws.artifacts(include: ["swapper"]).filterTypes(types: ["Expertise"]).asExpertise.{{ id }}.map { node(id: _.id).{{ ... on Expertise! }} })
  }
}
`
	// Keep this module's implementation distinct from the concurrently running
	// root-only Swapper fixtures: implementation cache aliases otherwise retain
	// their different workspace provenance, before nested ownership is tested.
	const fixtureState = `let nestedFixture: String! = "nested tool state"`
	initial := fmt.Sprintf(recomposeSource, fixtureState)
	fixture := recomposeFixture(c, initial).
		WithNewFile("dagger.toml", "[modules.swapper]\nsource = \".dagger/modules/swapper\"\n[modules.outer]\nsource = \"outer\"\n").
		WithNewFile("outer/dagger.json", `{"name":"outer","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("outer/main.dang", outerSource)
	ws := fixture.AsWorkspace()
	script := recomposeRecordingTurn(core.NewQuery(c).LLM(), "mutate nested", "advance", "readState")
	script = recomposeRecordingTurn(script, "updated nested", "added")
	script = recomposeRecordingTurn(script, "reset nested", "added")
	model := cannedRecordingModel(ctx, t, c, script)
	agent, llm := composeRecomposeFixture(ctx, t, c, ws, core.NewQuery(c).LLM(core.LLMOpts{Model: model}).WithWorkspace(ws), "outer")
	llm = llm.WithPrompt("mutate nested").Loop()
	transcript, err := llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "private counter: 1")

	updated := fmt.Sprintf(recomposeSource, fixtureState+`
  let addedDefault: String! = "nested default"
  added: String! { addedDefault + "; counter: " + toString(state) }
`)
	// Reloading refreshes the nested Swapper entry too: Outer's nested
	// compose recorded it on its own, and Swapper owns its binding.
	ws = ws.WithNewFile(recomposeModulePath, updated)
	llm, err = recomposeLLM(ctx, t, c, ws, llm, "outer")
	require.NoError(t, err)
	llm = llm.WithPrompt("updated nested").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "nested default; counter: 1", "reload must retain the nested Swapper tool state")

	// A nested binding still owns its explicit version contract after replay.
	portable, err := sink.captureLLMRecipe(ctx, t, c, llm)
	require.NoError(t, err)
	c = connect(ctx, t)
	_, llm = restoreComposer(ctx, t, c, agent, portable)
	ws = llm.Workspace().WithNewFile(recomposeModulePath,
		strings.ReplaceAll(updated, "base.withTools(currentNode)", "base.withTools(currentNode, version: 2)"))
	llm, err = recomposeLLM(ctx, t, c, ws, llm, "outer")
	require.NoError(t, err)
	llm = llm.WithPrompt("reset nested").Loop()
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "nested default; counter: 0")

	// Removing the nested call does not drop Swapper: it is recorded on its
	// own, and the reload refreshes it with its binding and state.
	withoutNested := ws.WithNewFile("outer/main.dang", strings.ReplaceAll(outerSource,
		`base.compose(expertise: ws.artifacts(include: ["swapper"]).filterTypes(types: ["Expertise"]).asExpertise.{{ id }}.map { node(id: _.id).{{ ... on Expertise! }} })`, "base"))
	llm, err = refreshLLM(ctx, c, withoutNested, llm)
	require.NoError(t, err)
	tools, err := llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## added\n", "the recorded Swapper entry keeps its tools")
}

func (LLMSuite) TestRecomposeNestedRecordedEntry(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	const outerSource = `
type Outer {
  agent(base: LLM!, ws: Workspace!): LLM! @agent {
    base.withSystemPrompt("outer original").compose(expertise: ws.artifacts(include: ["inner"]).filterTypes(types: ["Expertise"]).asExpertise.{{ id }}.map { node(id: _.id).{{ ... on Expertise! }} })
  }
}
`
	const innerSource = `
type Inner {
  agent(base: LLM!): LLM! @agent { base.withSystemPrompt("inner original") }
}
`
	fixture := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.outer]\nsource = \"outer\"\n[modules.inner]\nsource = \"inner\"\n").
		WithNewFile("outer/dagger.json", `{"name":"outer","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("outer/main.dang", outerSource).
		WithNewFile("inner/dagger.json", `{"name":"inner","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("inner/main.dang", innerSource)
	ws := fixture.AsWorkspace()
	// Inner is recorded on its own before Outer composes it: Outer's nested
	// compose leaves the recorded entry alone instead of running it again,
	// and never takes over the identical unowned prompt.
	agent, llm := composeRecomposeFixture(ctx, t, c, ws, core.NewQuery(c).LLM().WithWorkspace(ws).WithSystemPrompt("inner original"), "inner")
	llm, err := composeLLM(ctx, c, ws, llm, "outer")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		"inner original", "inner original", "outer original",
	}, recomposeSystemPrompts(ctx, t, c, llm))
	require.Len(t, recordedExpertise(ctx, t, c, llm), 2)

	// Composing Outer again refreshes only the Outer module, in place: the
	// Inner entry keeps its original code.
	ws = ws.WithNewFile("inner/main.dang", strings.ReplaceAll(innerSource, "inner original", "inner updated")).
		WithNewFile("outer/main.dang", strings.ReplaceAll(outerSource, "outer original", "outer updated"))
	llm, err = composeLLM(ctx, c, ws, llm, "outer")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"inner original", "inner original", "outer updated"}, recomposeSystemPrompts(ctx, t, c, llm),
		"Outer's refresh does not touch the Inner module")

	// Portable recipes retain module ownership and the record. Reloading
	// refreshes the Inner entry too.
	expected := []string{"inner original", "inner updated", "outer updated"}
	for range 2 {
		portable, err := sink.captureLLMRecipe(ctx, t, c, llm)
		require.NoError(t, err)
		c, sink = connectWithTrace(ctx, t)
		agent, llm = restoreComposer(ctx, t, c, agent, portable)
		ws = llm.Workspace()
		llm, err = refreshLLM(ctx, c, ws, llm)
		require.NoError(t, err)
		require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, c, llm))
	}

	// Composing Inner again refreshes it in place, not the identical
	// unowned prompt.
	llm, err = composeLLM(ctx, c, ws, llm, "inner")
	require.NoError(t, err)
	require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, c, llm))

	// Removing the nested compose changes only Outer's contribution.
	ws = ws.WithNewFile("outer/main.dang", `
type Outer {
  agent(base: LLM!): LLM! @agent { base.withSystemPrompt("outer alone") }
}
`)
	for range 2 {
		llm, err = composeLLM(ctx, c, ws, llm, "outer")
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"inner original", "inner updated", "outer alone"},
			recomposeSystemPrompts(ctx, t, c, llm))
	}
}

func (LLMSuite) TestRecomposeDirectCallerSkills(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	const shared = "Identical caller prompt."
	const original = `
  contribute(base: LLM!, skills: Directory!): LLM! {
    base.withSystemPrompt("Identical caller prompt.").withSkills(skills)
  }
`
	fixture := core.NewQuery(c).Directory().WithNewFile("dagger.toml", "[modules.owner]\nsource = \"owner\"\n[modules.owner-extra]\nsource = \"owner-extra\"\n")
	for _, module := range []struct{ name, typ string }{{"owner", "Owner"}, {"owner-extra", "OwnerExtra"}} {
		fixture = fixture.
			WithNewFile(module.name+"/dagger.json", fmt.Sprintf(`{"name":%q,"engineVersion":"v1.0.0-0","sdk":"dang"}`, module.name)).
			WithNewFile(module.name+"/main.dang", "type "+module.typ+" {"+original+"}")
	}
	ws := fixture.AsWorkspace()
	for _, name := range []string{"owner", "owner-extra"} {
		require.NoError(t, ws.ModuleSource(name).AsModule().Serve(ctx))
	}
	manual := core.NewQuery(c).Directory().WithNewFile("manual/SKILL.md", "---\ndescription: Main client skill.\n---\nmanual")
	oldSkills := core.NewQuery(c).Directory().WithNewFile("old-owned/SKILL.md", "---\ndescription: Old module skill.\n---\nold")
	base := core.NewQuery(c).LLM().WithWorkspace(ws).WithSystemPrompt(shared).WithSkills(manual)
	baseID, err := base.ID(ctx)
	require.NoError(t, err)
	skillsID, err := oldSkills.ID(ctx)
	require.NoError(t, err)
	seeds := map[string]*core.LLM{}
	for _, caller := range []string{"owner", "ownerExtra"} {
		var res struct {
			Caller struct{ Contribute struct{ ID string } }
		}
		// No @agent entrypoint is involved. Both callers make identical core
		// calls on the same base with the same explicit arguments.
		require.NoError(t, c.Do(ctx, &dagger.Request{
			Query: fmt.Sprintf(`query($base: ID!, $skills: ID!) {
				caller: %s { contribute(base: $base, skills: $skills) { id } }
			}`, caller),
			Variables: map[string]any{"base": baseID, "skills": skillsID},
		}, &dagger.Response{Data: &res}))
		seeds[caller] = core.Ref[*core.LLM](core.NewQuery(c), core.ID(res.Caller.Contribute.ID))
		require.ElementsMatch(t, []string{shared, shared}, recomposeSystemPrompts(ctx, t, c, seeds[caller]))
		require.Contains(t, skillIndex(ctx, t, seeds[caller]), "old-owned")
	}
	seeds["main"] = base.WithSystemPrompt(shared).WithSkills(oldSkills)

	// Introduce two entries in Owner. Contributions belong to the module whose
	// code makes them, entry or not: Owner's direct call owns its own.
	const updated = `
type Owner {
  first(base: LLM!): LLM! @agent {
    base.withSystemPrompt("first replacement").withSkills(directory.withNewFile("current/SKILL.md", "---\ndescription: Current module skill.\n---\ncurrent"))
  }
  second(base: LLM!): LLM! @agent {
    base.withSystemPrompt("second replacement").withSkills(directory.withNewFile("second/SKILL.md", "---\ndescription: Second entrypoint skill.\n---\nsecond")).withTools(currentNode)
  }
  ping: String! { "second entrypoint tool" }
}
`
	ws = ws.WithNewFile("owner/main.dang", updated)
	for _, caller := range []string{"owner", "ownerExtra", "main"} {
		// An agent owns the composed seed, and is restored with it in each
		// new session.
		agent, llm, err := spawnComposed(ctx, t, c, seeds[caller], nil)
		require.NoError(t, err)
		client := c
		captureSink := sink
		refreshedWS := ws
		for round := range 2 {
			llm, err = recomposeLLM(ctx, t, client, refreshedWS, llm, "owner")
			require.NoError(t, err)
			// On the second round, the first entrypoint has not yet restored
			// Owner's binding. Preservation must wait for the second one.
			tools, err := llm.Tools(ctx)
			require.NoError(t, err)
			require.Contains(t, tools, "## ping\n")
			expected := []string{shared, shared, "first replacement", "second replacement"}
			// Owner's direct call owns its contributions. New Owner entries do
			// not refresh the module, but once they are recorded, refreshing
			// them clears it: Owner's old contributions are gone. Other
			// callers' identical calls keep their own ownership.
			ownerRefreshed := caller == "owner" && round > 0
			if ownerRefreshed {
				expected = expected[1:]
			}
			require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, client, llm), "caller %s, round %d", caller, round)
			skills := skillIndex(ctx, t, llm)
			require.Equal(t, "Main client skill.", skills["manual"])
			if round == 0 {
				require.Equal(t, "Current module skill.", skills["current"])
			} else {
				require.NotContains(t, skills, "current", "removed skills must not accumulate across edits")
				require.Equal(t, "Current module skill.", skills["next"])
			}
			require.Equal(t, "Second entrypoint skill.", skills["second"])
			if ownerRefreshed {
				require.NotContains(t, skills, "old-owned", "a refreshed module's direct contributions do not accumulate")
			} else {
				require.Contains(t, skills, "old-owned", "contributions of modules that were not refreshed remain")
			}
			portable, err := captureSink.captureLLMRecipe(ctx, t, client, llm)
			require.NoError(t, err)
			client, captureSink = connectWithTrace(ctx, t)
			agent, llm = restoreComposer(ctx, t, client, agent, portable)
			require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, client, llm))
			require.Equal(t, skills, skillIndex(ctx, t, llm))
			refreshedWS = llm.Workspace().WithNewFile("owner/main.dang", strings.ReplaceAll(updated, "current/SKILL.md", "next/SKILL.md"))
		}
	}
}

// Contributions belong to the module whose code makes them. A contribution
// made inside a dependency's function belongs to the dependency, so it is not
// refreshed with the entry that called it: each refresh adds another. Entries
// that contribute from their own module's code are refreshed in place.
func (LLMSuite) TestRecomposeTransitiveHelperOwnership(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	const ownerSource = `
type Owner {
  viaHelper(base: LLM!): LLM! @agent { donor.contribute(base) }
  direct(base: LLM!): LLM! @agent { base.withSystemPrompt(donor.text) }
}
`
	const donorSource = `
type Donor {
  contribute(base: LLM!): LLM! {
    base.withSystemPrompt("shared helper original")
  }
  text: String! { "own prompt original" }
}
`
	ws := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.owner]\nsource = \"owner\"\n").
		WithNewFile("owner/dagger.json", `{"name":"owner","engineVersion":"v1.0.0-0","sdk":"dang","dependencies":[{"name":"donor","source":"donor"}]}`).
		WithNewFile("owner/main.dang", ownerSource).
		WithNewFile("owner/donor/dagger.json", `{"name":"donor","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("owner/donor/main.dang", donorSource).AsWorkspace()
	agent, llm := composeRecomposeFixture(ctx, t, c, ws, nil, "owner")
	require.ElementsMatch(t, []string{"shared helper original", "own prompt original"}, recomposeSystemPrompts(ctx, t, c, llm))
	require.Len(t, recordedExpertise(ctx, t, c, llm), 2)

	updated := ws.WithNewFile("owner/donor/main.dang", strings.NewReplacer(
		"shared helper original", "shared helper refreshed",
		"own prompt original", "own prompt refreshed").Replace(donorSource))
	llm, err := refreshLLM(ctx, c, updated, llm)
	require.NoError(t, err)
	expected := []string{"shared helper refreshed", "own prompt refreshed"}
	require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, c, llm),
		"the dependency's prompt, made while the entry ran, is replaced with the entry's own")

	// The same holds after replay in another session.
	portable, err := sink.captureLLMRecipe(ctx, t, c, llm)
	require.NoError(t, err)
	target := connect(ctx, t)
	_, restored := restoreComposer(ctx, t, target, agent, portable)
	require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, target, restored))
	restored, err = refreshLLM(ctx, target, restored.Workspace(), restored)
	require.NoError(t, err)
	require.ElementsMatch(t, expected, recomposeSystemPrompts(ctx, t, target, restored))
	require.Len(t, recordedExpertise(ctx, t, target, restored), 2)
}

func (LLMSuite) TestRecomposeWorkerIdentity(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	fixture, err := installAgents(t, c, "staff")
	require.NoError(t, err)
	workerScript := recomposeRecordingTurn(core.NewQuery(c).LLM(), "inspect worker", "parentName")
	// Recompose removes the original leading prompt and appends its replacement
	// during the reload tool call. Record that mid-conversation system message.
	reloadCall := recomposeTool("refresh_reload", "reload")
	parentCall := recomposeTool("refresh_parent", "parentName")
	workerScript = workerScript.WithPrompt("refresh worker").
		WithResponse([]core.LLMContentBlockInput{reloadCall}).
		WithSystemPrompt("Updated worker role: helper").
		WithToolResult(reloadCall.CallID, "", false).
		WithResponse([]core.LLMContentBlockInput{parentCall}).
		WithToolResult(parentCall.CallID, "", false).
		WithResponse([]core.LLMContentBlockInput{{Kind: core.LLMContentBlockKindText, Text: "refresh worker done"}})
	workerScript = recomposeRecordingTurn(workerScript, "restored worker", "parentName")
	workerModel := cannedRecordingModel(ctx, t, c, workerScript)
	source, err := fixture.File("../staff/main.dang").Contents(ctx)
	require.NoError(t, err)
	source = strings.ReplaceAll(source, "__WORKER_MODEL__", workerModel)
	ws := agentFixtureWorkspace(fixture.WithNewFile("../staff/main.dang", source))

	// Discovery remains directive-based even though explicit asExpertise is
	// signature-based. The plain worker transform must not become a chief role.
	_, defaultLLM := composeRecomposeFixture(ctx, t, c, ws, nil)
	defaults := recordedExpertise(ctx, t, c, defaultLLM)
	require.Len(t, defaults, 1)
	require.Contains(t, defaults[0].Artifact.URI, "staff/agent")
	chiefModel := cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(core.NewQuery(c).LLM(), "hire child", "hire"))
	chiefEntries, err := uriExpertiseIDs(ctx, c, ws, "staff/agent")
	require.NoError(t, err)
	chief := spawnWithExpertise(ctx, t, c, core.NewQuery(c).LLM(core.LLMOpts{Model: chiefModel}).WithWorkspace(ws), "chief", chiefEntries)
	root := chief.mustRun(ctx, t, "name handle parent { handle }")
	require.Equal(t, "chief", root.Get("name").String())
	require.Nil(t, root.Get("parent").Value(), "root agents have no parent")
	_, _, err = chief.sendAndWait(ctx, t, "hire child")
	require.NoError(t, err)
	transcript := chief.mustRun(ctx, t, "snapshot { transcript }").Get("snapshot.transcript").String()
	matches := regexp.MustCompile(`worker handle: ([a-zA-Z0-9_-]+)`).FindStringSubmatch(transcript)
	require.Len(t, matches, 2, transcript)
	worker := unmintedAgent(ctx, t, c, matches[1], "helper")
	lineage := worker.mustRun(ctx, t, "name parent { name handle }")
	require.Equal(t, "helper", lineage.Get("name").String())
	require.Equal(t, "chief", lineage.Get("parent.name").String())
	require.Equal(t, root.Get("handle").String(), lineage.Get("parent.handle").String())
	llm := core.Ref[*core.LLM](core.NewQuery(c), core.ID(worker.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
	entries := recordedExpertise(ctx, t, c, llm)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Artifact.URI, "staff/worker")
	require.Equal(t, []string{"Worker role: helper"}, recomposeSystemPrompts(ctx, t, c, llm))
	_, _, err = worker.sendAndWait(ctx, t, "inspect worker")
	require.NoError(t, err)
	require.Contains(t, worker.mustRun(ctx, t, "snapshot { transcript }").Get("snapshot.transcript").String(), "original parent: chief")

	updated := strings.NewReplacer("Worker role: ", "Updated worker role: ", "Original peer implementation.", "Updated peer implementation.", "original parent: ", "updated parent: ").Replace(source)
	llm = core.Ref[*core.LLM](core.NewQuery(c), core.ID(worker.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
	llmID, err := llm.WithWorkspace(ws.WithNewFile("../staff/main.dang", updated)).ID(ctx)
	require.NoError(t, err)
	require.NoError(t, worker.reseedAgent(ctx, t, string(llmID)))
	// Reload looks each recorded entry up again and composes them, executed
	// inside the worker's loop: the
	// conversation the tool receives is owned by the worker, so the worker
	// entry's me is the worker, never the chief that originally invoked
	// spawn. It refreshes only the recorded worker entry, so the worker
	// stays a worker and gains no chief tools.
	_, _, err = worker.sendAndWait(ctx, t, "refresh worker")
	require.NoError(t, err)
	llm = core.Ref[*core.LLM](core.NewQuery(c), core.ID(worker.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
	require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
	require.Equal(t, []string{"Updated worker role: helper"}, recomposeSystemPrompts(ctx, t, c, llm))
	tools, err := llm.Tools(ctx)
	require.NoError(t, err)
	require.NotContains(t, tools, "chiefOnly")
	// Read from outside any loop, the worker's conversation still lists its
	// Agent! tool: the conversation's owner supplies it.
	require.Contains(t, tools, "## parentName\n")
	require.Contains(t, tools, "Updated peer implementation.")
	transcript, err = llm.Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "updated parent: chief")
	// The refreshed system prompt moved to the end of the history, but the
	// conversation itself is unchanged: the reload is not reported as a
	// replaced history. Its withUserConfig workspace has the same contents,
	// so no (empty) workspace change is reported either.
	require.Contains(t, transcript, "Continuing from the returned conversation.")
	require.NotContains(t, transcript, "Conversation history replaced")
	require.NotContains(t, transcript, "Workspace changed")

	// CLI restore evaluates a captured conversation before minting its runtime.
	// Seed-free owner references must remain lazy through that evaluation.
	chiefSnapshot := core.Ref[*core.LLM](core.NewQuery(c), core.ID(chief.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
	chiefRecipe, err := sink.captureLLMRecipe(ctx, t, c, chiefSnapshot)
	require.NoError(t, err)
	workerRecipe, err := sink.captureLLMRecipe(ctx, t, c, llm)
	require.NoError(t, err)
	t.Run("restore worker before recomposition", func(ctx context.Context, t *testctx.T) {
		target := connect(ctx, t)
		restoredChiefSeed, err := core.Ref[*core.LLM](core.NewQuery(target), chiefRecipe).ID(ctx)
		require.NoError(t, err)
		_, err = rehydrateAgent(ctx, target, string(restoredChiefSeed), root.Get("handle").String(), "chief", "IDLE", "")
		require.NoError(t, err)
		restoredWorkerSeed, err := core.Ref[*core.LLM](core.NewQuery(target), workerRecipe).ID(ctx)
		require.NoError(t, err, "load the snapshot before spawning its owning worker")
		restoredWorker, err := rehydrateAgentWithParent(ctx, target, string(restoredWorkerSeed), matches[1], "helper", "IDLE", "", root.Get("handle").String())
		require.NoError(t, err)
		_, _, err = restoredWorker.sendAndWait(ctx, t, "restored worker")
		require.NoError(t, err)
		restoredTranscript := restoredWorker.mustRun(ctx, t, "snapshot { transcript }").Get("snapshot.transcript").String()
		require.Equal(t, 2, strings.Count(restoredTranscript, "updated parent: chief"), "parentName must run again after restore")
		restored := core.Ref[*core.LLM](core.NewQuery(target), core.ID(restoredWorker.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
		require.Equal(t, entries, recordedExpertise(ctx, t, target, restored))
		restored = recomposeRecordedExpertise(ctx, t, target, restored.Workspace(), restored)
		require.Equal(t, []string{"Updated worker role: helper"}, recomposeSystemPrompts(ctx, t, target, restored))
		restoredTools, err := restored.Tools(ctx)
		require.NoError(t, err)
		require.NotContains(t, restoredTools, "chiefOnly")
	})

	// Composing the chief entry into the worker's conversation appends it: the
	// worker becomes a chief.
	llm, err = composeURI(ctx, c, llm.Workspace(), llm, "staff/agent")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"Updated worker role: helper", "Chief role."}, recomposeSystemPrompts(ctx, t, c, llm))
	tools, err = llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## chiefOnly\n")
	require.Len(t, recordedExpertise(ctx, t, c, llm), 2)
	llm = recomposeRecordedExpertise(ctx, t, c, llm.Workspace(), llm)
	require.ElementsMatch(t, []string{"Updated worker role: helper", "Chief role."}, recomposeSystemPrompts(ctx, t, c, llm))

	// me is the owner of the conversation being composed, whoever composes
	// it. This client is neither agent: composing the worker entry into the
	// chief's conversation gives it the chief, while the worker's own
	// conversation above keeps the worker.
	chiefLLM, err := composeURI(ctx, c, ws, chiefSnapshot, "staff/worker")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"Chief role.", "Worker role: chief"}, recomposeSystemPrompts(ctx, t, c, chiefLLM))
	require.Len(t, recordedExpertise(ctx, t, c, chiefLLM), 2)
}

func spawnWithExpertise(ctx context.Context, t *testctx.T, c *dagger.Client, llm *core.LLM, name string, entries []core.ID) *agentHandle {
	t.Helper()
	id, err := llm.ID(ctx)
	require.NoError(t, err)
	var result struct{ Node struct{ Spawn string } }
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!, $name: String!, $entries: [ID!]!) { node(id: $id) { ... on LLM { spawn(name: $name, expertise: $entries) } } }`,
		Variables: map[string]any{"id": id, "name": name, "entries": entries},
	}, &dagger.Response{Data: &result}))
	return &agentHandle{c: c, agentID: result.Node.Spawn}
}

func (LLMSuite) TestComposeTransformURIIdentity(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	const source = `
type Swapper {
  first(base: LLM!): LLM! { base.withSystemPrompt("first original") }
  second(base: LLM!): LLM! { base.withSystemPrompt("second original") }
  invalid: Directory! { directory }
}
`
	ws := recomposeFixture(c, source).AsWorkspace()
	// Neither transform carries @agent, so default discovery finds nothing.
	// Recomposing an empty record, or composing no entries, is a no-op.
	agent, llm := composeRecomposeFixture(ctx, t, c, ws, nil)
	require.Empty(t, recordedExpertise(ctx, t, c, llm))
	for _, noop := range []func() (*core.LLM, error){
		func() (*core.LLM, error) { return refreshLLM(ctx, c, ws, llm) },
		func() (*core.LLM, error) { return composeInto(ctx, c, llm, nil) },
	} {
		unchanged, err := noop()
		require.NoError(t, err)
		require.Empty(t, recordedExpertise(ctx, t, c, unchanged))
		require.Empty(t, recomposeSystemPrompts(ctx, t, c, unchanged))
	}
	// Explicit conversion uses signatures.
	llm, err := composeURI(ctx, c, ws, llm, "swapper/first")
	require.NoError(t, err)
	entries := recordedExpertise(ctx, t, c, llm)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Artifact.URI, "swapper/first")
	// Composing a recorded entry again, by its full URI or twice in one
	// batch, refreshes it in place instead of failing as a duplicate.
	again, err := composeURI(ctx, c, ws, llm, entries[0].Artifact.URI)
	require.NoError(t, err, "an absolute URI identifies the same entry")
	require.Equal(t, entries, recordedExpertise(ctx, t, c, again))
	require.Equal(t, []string{"first original"}, recomposeSystemPrompts(ctx, t, c, again))
	ids, err := uriExpertiseIDs(ctx, c, ws, "swapper/first")
	require.NoError(t, err)
	require.Len(t, ids, 1)
	twice, err := composeInto(ctx, c, core.NewQuery(c).LLM().WithWorkspace(ws), []core.ID{ids[0], ids[0]})
	require.NoError(t, err)
	require.Equal(t, entries, recordedExpertise(ctx, t, c, twice))
	require.Equal(t, []string{"first original"}, recomposeSystemPrompts(ctx, t, c, twice))
	_, err = uriExpertiseIDs(ctx, c, ws, "swapper/invalid")
	require.Error(t, err, "asExpertise must reject non-transform artifacts")
	// A transform is listed without arguments, since composition supplies
	// its conversation. Evaluated on its own, it says how to use it.
	_, err = ws.Artifacts().FilterURI("swapper/first").One().Value().ID(ctx)
	require.ErrorContains(t, err, `transforms a conversation: compose it with LLM.compose or LLM.spawn(expertise:)`)
	require.ErrorContains(t, err, `"base" argument`)
	// A new entry is appended after the recorded ones.
	llm, err = composeURI(ctx, c, ws, llm, "swapper/second")
	require.NoError(t, err)
	entries = recordedExpertise(ctx, t, c, llm)
	require.Len(t, entries, 2)
	require.Contains(t, entries[0].Artifact.URI, "swapper/first")
	require.Contains(t, entries[1].Artifact.URI, "swapper/second")

	// Composing refreshes only the given entry, in place, and runs it as
	// given: here, as converted from the original workspace. Other recorded
	// entries are untouched until a reload.
	ws = ws.WithNewFile(recomposeModulePath, strings.ReplaceAll(source, "original", "updated"))
	llm, err = composeInto(ctx, c, llm.WithWorkspace(ws), ids)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"first original", "second original"}, recomposeSystemPrompts(ctx, t, c, llm))
	require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
	llm, err = composeURI(ctx, c, ws, llm, "swapper/first")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"first updated", "second original"}, recomposeSystemPrompts(ctx, t, c, llm))
	require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
	llm, err = refreshLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"first updated", "second updated"}, recomposeSystemPrompts(ctx, t, c, llm))
	require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
	t.Run("portable replay", func(ctx context.Context, t *testctx.T) {
		portable, err := sink.captureLLMRecipe(ctx, t, c, llm)
		require.NoError(t, err)
		target := connect(ctx, t)
		_, restored := restoreComposer(ctx, t, target, agent, portable)
		require.Equal(t, entries, recordedExpertise(ctx, t, target, restored))
		restored = recomposeRecordedExpertise(ctx, t, target, restored.Workspace(), restored)
		require.Equal(t, entries, recordedExpertise(ctx, t, target, restored))
		require.ElementsMatch(t, []string{"first updated", "second updated"}, recomposeSystemPrompts(ctx, t, target, restored))
	})
}

func (LLMSuite) TestRecomposeRefindsOrFallsBack(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const source = `
type Swapper {
  first(base: LLM!): LLM! { base.withSystemPrompt("first original") }
}
`
	ws := recomposeFixture(c, source).AsWorkspace()
	_, llm := spawnURI(ctx, t, c, ws, core.NewQuery(c).LLM(), "swapper/first")
	entries := recordedExpertise(ctx, t, c, llm)
	require.Len(t, entries, 1)

	// The bound workspace addresses the entry: it is found again there.
	edited := ws.WithNewFile(recomposeModulePath, strings.ReplaceAll(source, "original", "edited"))
	llm, err := refreshLLM(ctx, c, edited, llm)
	require.NoError(t, err)
	require.Equal(t, []string{"first edited"}, recomposeSystemPrompts(ctx, t, c, llm))
	require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))

	// Another repository's workspace does not address the entry, as after
	// switching to its checkout. The recorded entry reruns from its own
	// source rather than being dropped or failing.
	other := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.unrelated]\nsource = \"unrelated\"\n").
		WithNewFile("unrelated/dagger.json", `{"name":"unrelated","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("unrelated/main.dang", "type Unrelated {\n  ping: String! { \"pong\" }\n}\n").
		AsWorkspace()
	llm, err = refreshLLM(ctx, c, other, llm)
	require.NoError(t, err)
	require.Equal(t, []string{"first edited"}, recomposeSystemPrompts(ctx, t, c, llm))
	require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
	_, err = llm.Workspace().File("unrelated/main.dang").Contents(ctx)
	require.NoError(t, err, "reload keeps the conversation's bound workspace")

	// Bound to a workspace that addresses it again, the entry is found there.
	returned := edited.WithNewFile(recomposeModulePath, strings.ReplaceAll(source, "original", "returned"))
	llm, err = refreshLLM(ctx, c, returned, llm)
	require.NoError(t, err)
	require.Equal(t, []string{"first returned"}, recomposeSystemPrompts(ctx, t, c, llm))
	require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
}

// A recorded entry's module that fails to load in the bound workspace makes
// the reload lookup raise its load error, instead of the lookup coming back
// empty and the recorded entry silently rerunning from its old source.
func (LLMSuite) TestReloadLookupRaisesLoadError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const source = `
type Swapper {
  first(base: LLM!): LLM! { base.withSystemPrompt("first original") }
}
`
	ws := recomposeFixture(c, source).AsWorkspace()
	_, llm := spawnURI(ctx, t, c, ws, core.NewQuery(c).LLM(), "swapper/first")
	entries := recordedExpertise(ctx, t, c, llm)
	require.Len(t, entries, 1)

	broken := ws.WithNewFile(recomposeModulePath, "type Swapper {\n  first: IntentionallyUndefinedType! { \"hi\" }\n}\n")
	_, err := uriExpertiseIDs(ctx, c, broken, entries[0].Artifact.URI)
	require.ErrorContains(t, err, "IntentionallyUndefinedType", "the lookup itself raises the load error")
	_, err = refreshLLM(ctx, c, broken, llm)
	require.ErrorContains(t, err, "IntentionallyUndefinedType")
	require.Equal(t, []string{"first original"}, recomposeSystemPrompts(ctx, t, c, llm), "the conversation is unchanged")
}

func (LLMSuite) TestComposeTransformDimensionIdentity(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const source = `
type Swapper {
  members: Members! { Members() }
}
type Members @collection {
  names: [String!]! @keys
  new() {
    self.names = ["one", "two"]
    self
  }
  member(name: String!): Member! @get { Member(name) }
}
type Member {
  let name: String!
  new(name: String!) {
    self.name = name
    self
  }
  transform(base: LLM!): LLM! { base.withSystemPrompt("original member: " + name) }
}
`
	ws := recomposeFixture(c, source).AsWorkspace()
	artifacts, err := ws.Artifacts().FilterTypes([]string{"LLM"}).Items(ctx)
	require.NoError(t, err)
	require.Len(t, artifacts, 2)
	firstURI, err := artifacts[0].URI(ctx)
	require.NoError(t, err)
	secondURI, err := artifacts[1].URI(ctx)
	require.NoError(t, err)
	_, llm := spawnURI(ctx, t, c, ws, core.NewQuery(c).LLM(), firstURI)
	llm, err = composeURI(ctx, c, ws, llm, secondURI)
	require.NoError(t, err)
	entries := recordedExpertise(ctx, t, c, llm)
	require.Len(t, entries, 2)
	require.Contains(t, entries[0].Artifact.URI, "member=one")
	require.Contains(t, entries[1].Artifact.URI, "member=two")
	require.ElementsMatch(t, []string{"original member: one", "original member: two"}, recomposeSystemPrompts(ctx, t, c, llm))
	// The reload lookup finds each entry again by its URI, dimension keys included.
	ws = ws.WithNewFile(recomposeModulePath, strings.ReplaceAll(source, "original member", "updated member"))
	llm, err = refreshLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
	require.ElementsMatch(t, []string{"updated member: one", "updated member: two"}, recomposeSystemPrompts(ctx, t, c, llm))
	// A collection item the workspace no longer has is not addressed there:
	// it reruns from its recorded source while its sibling is found again.
	ws = ws.WithNewFile(recomposeModulePath, strings.NewReplacer(`["one", "two"]`, `["one"]`, "original member", "renamed member").Replace(source))
	llm, err = refreshLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
	require.ElementsMatch(t, []string{"renamed member: one", "updated member: two"}, recomposeSystemPrompts(ctx, t, c, llm))
}

func (AgentRuntimeSuite) TestParentLineageAndRestore(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	root := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "root"})
	rootInfo := root.mustRun(ctx, t, "handle parent { handle }")
	require.Nil(t, rootInfo.Get("parent").Value())
	rootHandle := rootInfo.Get("handle").String()
	child := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "child", handle: rootHandle + "-child", parentHandle: rootHandle})
	childInfo := child.mustRun(ctx, t, "handle parent { name handle parent { handle } }")
	require.Equal(t, rootHandle, childInfo.Get("parent.handle").String())
	require.Equal(t, "root", childInfo.Get("parent.name").String())
	require.Nil(t, childInfo.Get("parent.parent").Value())
	// Restore lineage independently of either conversation seed. In the fresh
	// session both runtimes are minted from the captured conversation recipes.
	rootSnapshot := core.Ref[*core.LLM](core.NewQuery(c), core.ID(root.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
	childSnapshot := core.Ref[*core.LLM](core.NewQuery(c), core.ID(child.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
	rootRecipe, err := sink.captureLLMRecipe(ctx, t, c, rootSnapshot)
	require.NoError(t, err)
	childRecipe, err := sink.captureLLMRecipe(ctx, t, c, childSnapshot)
	require.NoError(t, err)
	target := connect(ctx, t)
	restoredRoot, err := rehydrateAgent(ctx, target, string(rootRecipe), rootHandle, "root", "IDLE", "")
	require.NoError(t, err)
	restoredChild, err := rehydrateAgentWithParent(ctx, target, string(childRecipe), childInfo.Get("handle").String(), "child", "IDLE", "", rootHandle)
	require.NoError(t, err)
	require.Nil(t, restoredRoot.mustRun(ctx, t, "parent { handle }").Get("parent").Value())
	restored := restoredChild.mustRun(ctx, t, "parent { name handle parent { handle } }")
	require.Equal(t, rootHandle, restored.Get("parent.handle").String())
	require.Equal(t, "root", restored.Get("parent.name").String())
	require.Nil(t, restored.Get("parent.parent").Value())
}

func (LLMSuite) TestSpawnExpertiseFailureCanRetry(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const source = `
type Swapper {
  broken(base: LLM!, me: Agent!): LLM! { raise "composition deliberately failed" }
  ready(base: LLM!, me: Agent!): LLM! { base.withSystemPrompt("ready: " + me.name) }
  fresh(base: LLM!): LLM! { llm(model: "recording/W10=") }
  family(base: LLM!): LLM! {
    let child = llm(model: "recording/W10=").withWorkspace(base.workspace).spawn(name: "nested-child", expertise: base.workspace.artifacts.filterUri("swapper/ready").asExpertise.{{ id }}.map { node(id: _.id).{{ ... on Expertise! }} })
    base.withSystemPrompt("nested child: " + child.handle)
  }
}
`
	ws := recomposeFixture(c, source).AsWorkspace()
	base := core.NewQuery(c).LLM(core.LLMOpts{Model: emptyReplayModel}).WithWorkspace(ws)
	id, err := base.ID(ctx)
	require.NoError(t, err)
	broken, err := uriExpertiseIDs(ctx, c, ws, "swapper/broken")
	require.NoError(t, err)
	var result struct{ Node struct{ Spawn string } }
	err = c.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!, $entries: [ID!]!) { node(id: $id) { ... on LLM { spawn(name: "retry", expertise: $entries) } } }`,
		Variables: map[string]any{"id": id, "entries": broken},
	}, &dagger.Response{Data: &result})
	require.ErrorContains(t, err, "composition deliberately failed")
	ready, err := uriExpertiseIDs(ctx, c, ws, "swapper/ready")
	require.NoError(t, err)
	retry := spawnWithExpertise(ctx, t, c, base, "retry", ready)
	require.Equal(t, "retry", retry.mustRun(ctx, t, "name").Get("name").String())
	retrySnapshot := core.Ref[*core.LLM](core.NewQuery(c), core.ID(retry.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
	require.Contains(t, recomposeSystemPrompts(ctx, t, c, retrySnapshot), "ready: retry")

	t.Run("fresh conversation retains spawn owner", func(ctx context.Context, t *testctx.T) {
		fresh, err := uriExpertiseIDs(ctx, c, ws, "swapper/fresh")
		require.NoError(t, err)
		require.Len(t, fresh, 1)
		require.Len(t, ready, 1)
		agent := spawnWithExpertise(ctx, t, c, base, "fresh-agent", []core.ID{fresh[0], ready[0]})
		snapshot := core.Ref[*core.LLM](core.NewQuery(c), core.ID(agent.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
		require.Contains(t, recomposeSystemPrompts(ctx, t, c, snapshot), "ready: fresh-agent")
	})

	t.Run("nested spawn has its own composition record", func(ctx context.Context, t *testctx.T) {
		family, err := uriExpertiseIDs(ctx, c, ws, "swapper/family")
		require.NoError(t, err)
		parent := spawnWithExpertise(ctx, t, c, base, "family", family)
		snapshot := core.Ref[*core.LLM](core.NewQuery(c), core.ID(parent.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
		prompts := recomposeSystemPrompts(ctx, t, c, snapshot)
		require.Len(t, prompts, 1)
		require.True(t, strings.HasPrefix(prompts[0], "nested child: "))
		child := unmintedAgent(ctx, t, c, strings.TrimPrefix(prompts[0], "nested child: "), "nested-child")
		childSnapshot := core.Ref[*core.LLM](core.NewQuery(c), core.ID(child.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
		entries := recordedExpertise(ctx, t, c, childSnapshot)
		require.Len(t, entries, 1)
		require.Contains(t, entries[0].Artifact.URI, "swapper/ready")
		require.Contains(t, recomposeSystemPrompts(ctx, t, c, childSnapshot), "ready: nested-child")
		require.Equal(t, "family", child.mustRun(ctx, t, "parent { name }").Get("parent.name").String())
	})
}

// Spawning mints a new agent every time, and its entries run again on a
// conversation owned by that agent. The module calls they make that do not
// depend on the agent, such as tool constructors and helpers, must still be
// cached across spawns and sessions like any other module call.
func (LLMSuite) TestSpawnCompositionCaches(ctx context.Context, t *testctx.T) {
	fixture := func(c *dagger.Client) *core.Workspace {
		return core.NewQuery(c).Directory().
			WithNewFile("dagger.toml", "[modules.probe]\nsource = \"probe\"\n").
			WithNewFile("probe/dagger.json", `{"name":"probe","engineVersion":"v1.0.0-0","sdk":"dang","dependencies":[{"name":"stamper","source":"stamper"}]}`).
			WithNewFile("probe/main.dang", `
type Probe {
  agent(base: LLM!): LLM! @agent {
    base.withSystemPrompt("entry: " + currentTimestamp + "; helper: " + stamper.stamp)
  }
  worker(base: LLM!, me: Agent!): LLM! {
    base.withSystemPrompt("worker " + me.name + "; helper: " + stamper.stamp)
  }
}
`).
			WithNewFile("probe/stamper/dagger.json", `{"name":"stamper","engineVersion":"v1.0.0-0","sdk":"dang"}`).
			WithNewFile("probe/stamper/main.dang", `
type Stamper {
  stamp: String! { currentTimestamp }
}
`).AsWorkspace()
	}
	spawnPrompt := func(c *dagger.Client, uri, name string) (string, string) {
		ws := fixture(c)
		entries, err := uriExpertiseIDs(ctx, c, ws, uri)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		agent := spawnWithExpertise(ctx, t, c, core.NewQuery(c).LLM().WithWorkspace(ws), name, entries)
		seed := core.Ref[*core.LLM](core.NewQuery(c), core.ID(agent.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
		// Timestamps have a resolution of one second: a call that runs again
		// must be able to tell itself apart from the first run.
		time.Sleep(1100 * time.Millisecond)
		prompts := recomposeSystemPrompts(ctx, t, c, seed)
		require.Len(t, prompts, 1)
		entry, helper, ok := strings.Cut(prompts[0], "; helper: ")
		require.True(t, ok, prompts[0])
		return entry, helper
	}

	c := connect(ctx, t)
	firstEntry, firstHelper := spawnPrompt(c, "probe/agent", "first")
	for _, client := range []*dagger.Client{c, connect(ctx, t)} {
		// A second spawn in the same session, or a relaunched dagger agent.
		entry, helper := spawnPrompt(client, "probe/agent", "again")
		require.NotEqual(t, firstEntry, entry, "the entry receives a conversation owned by the new agent")
		require.Equal(t, firstHelper, helper, "module calls made by the entry are cached across spawns")
	}

	// The same for an entry taking the agent: it runs for each agent, but
	// the helper calls that do not take the agent are cached.
	alice, aliceHelper := spawnPrompt(c, "probe/worker", "alice")
	bob, bobHelper := spawnPrompt(c, "probe/worker", "bob")
	require.Equal(t, "worker alice", alice)
	require.Equal(t, "worker bob", bob)
	require.Equal(t, aliceHelper, bobHelper)
}

func (LLMSuite) TestComposeTransformReceivesAgent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const source = `
type Swapper {
  required(base: LLM!, me: Agent!): LLM! { base.withSystemPrompt("required: " + me.name) }
  optional(base: LLM!, me: Agent): LLM! {
    if (me == null) {
      base.withSystemPrompt("optional: no agent")
    } else {
      base.withSystemPrompt("optional: " + me.name)
    }
  }
}
`
	ws := recomposeFixture(c, source).AsWorkspace()
	required, err := uriExpertiseIDs(ctx, c, ws, "swapper/required")
	require.NoError(t, err, "conversion does not require an agent to have been minted yet")
	require.Len(t, required, 1)
	optional, err := uriExpertiseIDs(ctx, c, ws, "swapper/optional")
	require.NoError(t, err)
	require.Len(t, optional, 1)
	both := []core.ID{required[0], optional[0]}

	// Every Agent argument, required or optional, receives the agent that
	// owns the conversation, although this client composes it.
	receiver := spawnWithExpertise(ctx, t, c, core.NewQuery(c).LLM().WithWorkspace(ws), "receiver", []core.ID{})
	owned := core.Ref[*core.LLM](core.NewQuery(c), core.ID(receiver.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
	llm, err := composeInto(ctx, c, owned, both)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"required: receiver", "optional: receiver"}, recomposeSystemPrompts(ctx, t, c, llm))
	require.Len(t, recordedExpertise(ctx, t, c, llm), 2)
	llm, err = reloadConversation(ctx, c, llm)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"required: receiver", "optional: receiver"}, recomposeSystemPrompts(ctx, t, c, llm))

	// The same conversation without its owner, as a reload can meet it: the
	// recorded required-Agent entry keeps its record and contribution rather
	// than being stripped. It cannot rerun, so its module is not refreshed
	// at all: the optional entry of the same module keeps its contribution.
	llmID, err := llm.ID(ctx)
	require.NoError(t, err)
	var res struct {
		Node struct{ Unowned struct{ ID core.ID } }
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($llm: ID!) { node(id: $llm) { ... on LLM { unowned: __withOwnerAgent(handle: "") { id } } } }`,
		Variables: map[string]any{"llm": llmID},
	}, &dagger.Response{Data: &res}))
	disowned, err := reloadConversation(ctx, c, core.Ref[*core.LLM](core.NewQuery(c), res.Node.Unowned.ID))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"required: receiver", "optional: receiver"}, recomposeSystemPrompts(ctx, t, c, disowned))
	require.Equal(t, recordedExpertise(ctx, t, c, llm), recordedExpertise(ctx, t, c, disowned))

	// A conversation without an owner has no agent to give. An entry
	// requiring one is skipped: it does not run and is not recorded. An
	// optional Agent argument receives null.
	unowned := core.NewQuery(c).LLM().WithWorkspace(ws)
	llm, err = composeInto(ctx, c, unowned, both)
	require.NoError(t, err)
	require.Equal(t, []string{"optional: no agent"}, recomposeSystemPrompts(ctx, t, c, llm))
	entries := recordedExpertise(ctx, t, c, llm)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Artifact.URI, "swapper/optional")
	llm, err = reloadConversation(ctx, c, llm)
	require.NoError(t, err)
	require.Equal(t, []string{"optional: no agent"}, recomposeSystemPrompts(ctx, t, c, llm))
	require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
	llm, err = composeInto(ctx, c, unowned, required)
	require.NoError(t, err)
	require.Empty(t, recomposeSystemPrompts(ctx, t, c, llm))
	require.Empty(t, recordedExpertise(ctx, t, c, llm))
}

func (LLMSuite) TestRecomposeDirectCallerOwnsForeignTools(ctx context.Context, t *testctx.T) {
	c, sink := connectWithTrace(ctx, t)
	fixture := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.owner]\nsource = \"owner\"\n[modules.donor]\nsource = \"owner/donor\"\n").
		WithNewFile("owner/dagger.json", `{"name":"owner","engineVersion":"v1.0.0-0","sdk":"dang","dependencies":[{"name":"donor","source":"donor"}]}`).
		WithNewFile("owner/main.dang", `
type Owner {
  contribute(base: LLM!): LLM! { base.withTools(donor) }
}
`).
		WithNewFile("owner/donor/dagger.json", `{"name":"donor","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("owner/donor/main.dang", `
type Donor {
  agent(base: LLM!): LLM! @agent { base }
  ping: String! { "foreign tool" }
}
`)
	ws := fixture.AsWorkspace()
	require.NoError(t, ws.ModuleSource("owner").AsModule().Serve(ctx))
	baseID, err := core.NewQuery(c).LLM().WithWorkspace(ws).ID(ctx)
	require.NoError(t, err)
	var res struct {
		Owner struct{ Contribute struct{ ID string } }
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($base: ID!) { owner { contribute(base: $base) { id } } }`,
		Variables: map[string]any{"base": baseID},
	}, &dagger.Response{Data: &res}))
	llm := core.Ref[*core.LLM](core.NewQuery(c), core.ID(res.Owner.Contribute.ID))
	tools, err := llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## ping\n")
	agent, llm, err := spawnComposed(ctx, t, c, llm, nil)
	require.NoError(t, err)

	// The bound object's defining module is not the caller. Selecting Donor
	// must leave the binding installed by a direct call inside Owner alone.
	llm, err = recomposeLLM(ctx, t, c, ws, llm, "donor")
	require.NoError(t, err)
	tools, err = llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## ping\n")
	portable, err := sink.captureLLMRecipe(ctx, t, c, llm)
	require.NoError(t, err)
	c = connect(ctx, t)
	_, llm = restoreComposer(ctx, t, c, agent, portable)

	// Owner did not need an entry when it installed the binding: Owner owns
	// it. Adding Owner's first entry does not refresh the module, so the
	// binding survives.
	ws = llm.Workspace().WithNewFile("owner/main.dang", `
type Owner {
  agent(base: LLM!): LLM! @agent { base }
}
`)
	llm, err = composeLLM(ctx, c, ws, llm, "owner")
	require.NoError(t, err)
	tools, err = llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## ping\n", "a new entry does not refresh its module")

	// Refreshing Owner clears its contributions and reruns its entries.
	// Dropping its foreign tool must be rejected rather than silently
	// treating the binding as unowned or Donor-owned.
	_, err = refreshLLM(ctx, c, ws, llm)
	require.ErrorContains(t, err, "discard tool state")
	tools, err = llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## ping\n", "failed refresh leaves the old binding usable")

	// An entry rebinding the same type from Owner's code keeps the binding.
	claiming := ws.WithNewFile("owner/main.dang", `
type Owner {
  agent(base: LLM!): LLM! @agent { base.withTools(donor) }
}
`)
	llm, err = refreshLLM(ctx, c, claiming, llm)
	require.NoError(t, err)
	tools, err = llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## ping\n")
}

// A tool continuation, e.g. a tool returning `llm.withTools(self)` to rebind
// its own state, is made by the tool's module: the binding stays owned by
// that module, so the entry that first bound it refreshes it with its state.
// (When ownership followed the running entry instead, such a continuation was
// unowned, and every later reload failed: vito/agents' committer tools.)
func (LLMSuite) TestRecomposeToolContinuationOwnership(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	source := fmt.Sprintf(recomposeSource, `
  rebind(llm: LLM!): LLM! {
    llm.withTools(currentNode.{{... on Dagger.Swapper!}}.advance, version: 1)
  }
`)
	source = strings.ReplaceAll(source, "base.withTools(currentNode)", "base.withTools(currentNode, version: 1)")
	ws := recomposeFixture(c, source).AsWorkspace()
	require.NoError(t, ws.ModuleSource(".dagger/modules/swapper").AsModule().Serve(ctx))
	_, llm := composeRecomposeFixture(ctx, t, c, ws, nil)

	// What the model does when it calls rebind as a tool: the module function
	// runs with the conversation and returns a continuation.
	llmID, err := llm.ID(ctx)
	require.NoError(t, err)
	var res struct {
		Swapper struct{ Rebind struct{ ID core.ID } }
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($llm: ID!) { swapper { rebind(llm: $llm) { id } } }`,
		Variables: map[string]any{"llm": llmID},
	}, &dagger.Response{Data: &res}))
	continued := core.Ref[*core.LLM](core.NewQuery(c), res.Swapper.Rebind.ID)
	for range 2 {
		continued, err = refreshLLM(ctx, c, ws, continued)
		require.NoError(t, err, "reloading a module-owned continuation binding")
		model := cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(continued, "read state", "readState"))
		transcript, err := continued.WithModel(model).WithPrompt("read state").Loop().Transcript(ctx)
		require.NoError(t, err)
		require.Contains(t, transcript, "private counter: 1", "the continuation's state survives the reload")
	}
}

// A module installed under another name owns its contributions under that
// name, the same one its entries refresh: reloading replaces its prompt and
// rebinds its tools instead of accumulating them.
func (LLMSuite) TestRecomposeAliasedModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const probeSource = `
type Probe {
  let state: Int! = 0
  agent(base: LLM!): LLM! @agent {
    base.withSystemPrompt("probe prompt").withTools(currentNode)
  }
  advance: Probe! {
    state += 1
    self
  }
  readState: String! { "probe counter: " + toString(state) }
}
`
	ws := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.alias]\nsource = \"probe\"\n").
		WithNewFile("probe/dagger.json", `{"name":"probe","engineVersion":"v1.0.0-0","sdk":"dang"}`).
		WithNewFile("probe/main.dang", probeSource).
		AsWorkspace()
	_, llm := composeRecomposeFixture(ctx, t, c, ws, nil)
	entries := recordedExpertise(ctx, t, c, llm)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Artifact.URI, "alias", "the entry is addressed by the alias")
	require.Equal(t, []string{"probe prompt"}, recomposeSystemPrompts(ctx, t, c, llm))
	model := cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(llm, "advance", "advance"))
	llm = llm.WithModel(model).WithPrompt("advance").Loop()

	ws = ws.WithNewFile("probe/main.dang", strings.ReplaceAll(probeSource, `"probe prompt"`, `"edited probe prompt"`))
	for range 2 {
		var err error
		llm, err = refreshLLM(ctx, c, ws, llm)
		require.NoError(t, err)
		require.Equal(t, []string{"edited probe prompt"}, recomposeSystemPrompts(ctx, t, c, llm),
			"the aliased module's prompt is replaced, not duplicated")
		require.Equal(t, entries, recordedExpertise(ctx, t, c, llm))
	}
	model = cannedRecordingModel(ctx, t, c, recomposeRecordingTurn(llm, "read", "readState"))
	transcript, err := llm.WithModel(model).WithPrompt("read").Loop().Transcript(ctx)
	require.NoError(t, err)
	require.Contains(t, transcript, "probe counter: 1", "the aliased module's tool state is carried over")
}

// Staff's worker and chief roles are entries of one module (vito/agents#20,
// vito/agents#31), composed here without a model or a container runtime.
// Composing the chief entry onto a worker appends it without refreshing the
// staff module, so the worker keeps its role and gains the chief's; reloading
// the whole record then refreshes both staff entries together.
func (LLMSuite) TestComposeWorkerPromotion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", "[modules.staff]\nsource = \"staff\"\n").
		WithDirectory("staff", core.NewQuery(c).Host().Directory("./testdata/agents/staff")).
		AsWorkspace()
	workerEntries, err := uriExpertiseIDs(ctx, c, ws, "staff/worker")
	require.NoError(t, err)
	require.Len(t, workerEntries, 1)
	worker := spawnWithExpertise(ctx, t, c, core.NewQuery(c).LLM().WithWorkspace(ws), "helper", workerEntries)
	llm := core.Ref[*core.LLM](core.NewQuery(c), core.ID(worker.mustRun(ctx, t, "snapshot { id }").Get("snapshot.id").String()))
	workerRecord := recordedExpertise(ctx, t, c, llm)
	require.Len(t, workerRecord, 1)
	require.Equal(t, []string{"Worker role: helper"}, recomposeSystemPrompts(ctx, t, c, llm))

	// A reload keeps the worker a worker.
	llm, err = refreshLLM(ctx, c, ws, llm)
	require.NoError(t, err)
	require.Equal(t, workerRecord, recordedExpertise(ctx, t, c, llm))
	require.Equal(t, []string{"Worker role: helper"}, recomposeSystemPrompts(ctx, t, c, llm))
	tools, err := llm.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## parentName\n")
	require.NotContains(t, tools, "chiefOnly")

	// Promotion appends the chief entry; the worker entry does not run again.
	promoted, err := composeURI(ctx, c, ws, llm, "staff/agent")
	require.NoError(t, err)
	record := recordedExpertise(ctx, t, c, promoted)
	require.Len(t, record, 2)
	require.Equal(t, workerRecord[0], record[0])
	require.Equal(t, []string{"Worker role: helper", "Chief role."}, recomposeSystemPrompts(ctx, t, c, promoted))
	tools, err = promoted.Tools(ctx)
	require.NoError(t, err)
	require.Contains(t, tools, "## parentName\n")
	require.Contains(t, tools, "## chiefOnly\n")

	// A full reload, and composing the recorded chief entry again, each
	// refresh both staff entries together, in record order.
	reloaded, err := refreshLLM(ctx, c, ws, promoted)
	require.NoError(t, err)
	again, err := composeURI(ctx, c, ws, promoted, "staff/agent")
	require.NoError(t, err)
	for _, llm := range []*core.LLM{reloaded, again} {
		require.Equal(t, record, recordedExpertise(ctx, t, c, llm))
		require.Equal(t, []string{"Worker role: helper", "Chief role."}, recomposeSystemPrompts(ctx, t, c, llm))
		tools, err := llm.Tools(ctx)
		require.NoError(t, err)
		require.Contains(t, tools, "## parentName\n")
		require.Contains(t, tools, "## chiefOnly\n")
	}

	// Without an owner, the worker entry cannot rerun, so the staff module is
	// not refreshed: the record and both roles are kept.
	promotedID, err := promoted.ID(ctx)
	require.NoError(t, err)
	var res struct {
		Node struct{ Unowned struct{ ID core.ID } }
	}
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query:     `query($llm: ID!) { node(id: $llm) { ... on LLM { unowned: __withOwnerAgent(handle: "") { id } } } }`,
		Variables: map[string]any{"llm": promotedID},
	}, &dagger.Response{Data: &res}))
	disowned, err := refreshLLM(ctx, c, ws, core.Ref[*core.LLM](core.NewQuery(c), res.Node.Unowned.ID))
	require.NoError(t, err)
	require.Equal(t, record, recordedExpertise(ctx, t, c, disowned))
	require.Equal(t, []string{"Worker role: helper", "Chief role."}, recomposeSystemPrompts(ctx, t, c, disowned))
}
