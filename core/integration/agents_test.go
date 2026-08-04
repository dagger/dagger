package core

// These tests cover `dagger agent`, which discovers and composes module @agent
// middlewares (hack/designs/workspace-agents.md §3). They verify cross-module discovery, the base
// argument being matched by type (not name), nested discovery through
// object-returning functions, signature validation, and the composed toolset
// (auto-exclusion of the entrypoint + collision-driven namespacing). Driving the
// interactive prompt itself needs a live model and is covered by manual QA.
//
// See also:
// - checks_test.go: the @check sibling this machinery is cloned from.
// - workspace_modules_test.go: installing modules into workspaces.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type AgentsSuite struct{}

func TestAgents(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(AgentsSuite{})
}

// installAgents mounts the agents testdata and installs the named modules into a
// fresh /work/modules/app workspace.
func installAgents(t *testctx.T, c *dagger.Client, names ...string) (*dagger.Container, error) {
	env, err := specificTestEnv(t, c, "agents")
	if err != nil {
		return nil, err
	}
	var toml string
	for _, name := range names {
		toml += fmt.Sprintf("[modules.%s]\nsource = \"../%s\"\n", name, name)
	}
	return env.WithWorkdir("app").WithNewFile("dagger.toml", toml), nil
}

func (AgentsSuite) TestListAcrossModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := installAgents(t, c, "editor", "godoc")
	require.NoError(t, err)

	out, err := modGen.With(daggerExec("agent", "-l")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "editor:agent")
	// godoc's base argument is named `llm`, not `base`; it must still be
	// discovered, since the base is matched by type rather than name.
	require.Contains(t, out, "godoc:agent")
}

func (AgentsSuite) TestSelection(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := installAgents(t, c, "editor", "godoc")
	require.NoError(t, err)

	out, err := modGen.With(daggerExec("agent", "-l", "editor")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "editor:agent")
	require.NotContains(t, out, "godoc:agent")
}

func (AgentsSuite) TestNestedDiscovery(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := installAgents(t, c, "nested")
	require.NoError(t, err)

	// The @agent lives on NestedTools, reached via the object-returning function
	// Nested.tools; the rollup recurses through functions, so it is discoverable.
	out, err := modGen.With(daggerExec("agent", "-l")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "nested:tools:agent")
}

func (AgentsSuite) TestValidationRejectsExtraRequiredArg(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := installAgents(t, c, "badagent")
	require.NoError(t, err)

	// badagent's @agent declares a required `extra: String!` beyond its LLM base,
	// which must be rejected at module load.
	out, err := modGen.With(daggerExecFail("agent", "-l")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "may only require a single LLM! argument")
}

func (AgentsSuite) TestComposeToolset(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := installAgents(t, c, "editor", "godoc")
	require.NoError(t, err)

	out, err := modGen.
		With(daggerQuery(`{workspace: currentWorkspace{agents{compose{tools}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	// The @agent entrypoint is auto-excluded from the toolset, so authors don't
	// need `except: ["agent"]`.
	require.NotContains(t, out, "## agent")
	// Collision: both modules define `shared`. Instead of one silently
	// shadowing the other, ALL tools of BOTH conflicting modules are served
	// under namespaced names — the whole toolset, not just the colliding tool,
	// so each module's toolset stays uniform. (godoc's tools being present at
	// all proves the `llm`-named base was threaded correctly.)
	require.Contains(t, out, "## editor_shared")
	require.Contains(t, out, "shared tool from editor")
	require.Contains(t, out, "## godoc_shared")
	require.Contains(t, out, "shared tool from godoc")
	require.Contains(t, out, "## editor_readFile")
	require.Contains(t, out, "## godoc_goDoc")
	require.NotContains(t, out, "## readFile")
	require.NotContains(t, out, "## goDoc")
	require.NotContains(t, out, "## shared")

	// A module with no collisions keeps its bare tool names — namespacing only
	// kicks in when toolsets actually conflict.
	out, err = modGen.
		With(daggerQuery(`{workspace: currentWorkspace{agents(include:["editor"]){compose{tools}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "## readFile")
	require.Contains(t, out, "## shared")
	require.NotContains(t, out, "## editor_")
}

// TestComposeSeedIsWorkspaceBound locks in that compose's default base LLM is
// bound to the workspace the group was rolled up from. llm() starts unbound
// (NewLLM no longer binds the ambient workspace), so without the explicit
// withWorkspace seed, reading the composed LLM's workspace fails with "no
// workspace is bound to this LLM" — the `dagger agent` startup regression.
func (AgentsSuite) TestComposeSeedIsWorkspaceBound(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := installAgents(t, c, "editor")
	require.NoError(t, err)

	// Reading the bound workspace back out proves the seed bound one, and the
	// entries prove it's the env workspace the group was rolled up from (its
	// root carries the fixture module tree).
	out, err := modGen.
		With(daggerQuery(`{workspace: currentWorkspace{agents{compose{workspace{directory(path:"/"){entries}}}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "modules/")
}

// TestAgentReadsSeedWorkspace covers the mid-fold half of the same regression:
// an @agent leaf that reads base.workspace during compose (like a real agent
// scanning project context) must see the seed's bound workspace.
func (AgentsSuite) TestAgentReadsSeedWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := installAgents(t, c, "wsaware")
	require.NoError(t, err)

	// wsaware:agent derives its system prompt from
	// base.workspace.file("dagger.toml"), so composing succeeds only when the
	// seed is workspace-bound (compose runs each leaf eagerly).
	_, err = modGen.
		With(daggerQuery(`{workspace: currentWorkspace{agents{compose{tools}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
}

// editorSourceWithDoc is the editor fixture's source with readFile's doc string
// swapped for the given marker, used to prove overlay-staged module edits are
// visible to agent composition.
func editorSourceWithDoc(doc string) string {
	return fmt.Sprintf(`"""A basic coding agent, for @agent integration tests."""
type Editor {
  """Compose the editor's tools onto a base LLM. Base named `+"`base`"+`."""
  agent(base: LLM!): LLM! @agent {
    base
      .withTools(currentNode)
      .withSystemPrompt("editor agent system prompt")
  }

  """%s"""
  readFile(path: String!): String! {
    `+"`read ${path}`"+`
  }

  """shared tool from editor"""
  shared(x: String!): String! {
    `+"`editor shared ${x}`"+`
  }
}
`, doc)
}

// TestOverlayModuleSourceIsResolvedThroughOverlay pins the half of the overlay
// module machinery that works today: Workspace.moduleSource, resolved off a
// workspace carrying a staged (unexported) edit, loads the module from the
// overlay rather than from disk. workspaceOverlayModules (core/schema/
// workspace_overlay_modules.go) resolves its modules exactly this way.
func (AgentsSuite) TestOverlayModuleSourceIsResolvedThroughOverlay(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := installAgents(t, c, "editor")
	require.NoError(t, err)

	const marker = "OVERLAY EDITED DOC MARKER"
	out, err := modGen.
		With(daggerQuery(
			`{workspace: currentWorkspace{withNewFile(path:"/modules/editor/main.dang", contents:%s){moduleSource(path:"/modules/editor"){asModule{name objects{asObject{name functions{name description}}}}}}}}`,
			strconv.Quote(editorSourceWithDoc(marker)),
		)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, marker)
	require.NotContains(t, out, "Read a file (stub).")
}

// TestOverlayModuleSourceEdit locks in the self-repair loop: an agent that
// edits a module's source and recomposes itself must see its own STAGED edit,
// with nothing exported to disk. The edit is staged and the toolset selected in
// a single query, off the Workspace returned by withNewFile. Two layers
// cooperate: workspaceOverlayModules re-resolves the module through the overlay
// for composition (Workspace.agents), and WorkspaceServedSchema layers the same
// overlay modules onto the served schema LLM.tools renders from.
func (AgentsSuite) TestOverlayModuleSourceEdit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := installAgents(t, c, "editor")
	require.NoError(t, err)

	const marker = "OVERLAY EDITED DOC MARKER"

	out, err := modGen.
		With(daggerQuery(
			`{workspace: currentWorkspace{withNewFile(path:"/modules/editor/main.dang", contents:%s){agents{compose{tools}}}}}`,
			strconv.Quote(editorSourceWithDoc(marker)),
		)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "## readFile")
	require.Contains(t, out, marker)
	require.NotContains(t, out, "Read a file (stub).")

	// Control: the un-staged workspace still composes the on-disk source.
	out, err = modGen.
		With(daggerQuery(`{workspace: currentWorkspace{agents{compose{tools}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Read a file (stub).")
	require.NotContains(t, out, marker)
}

// TestOverlayWithModule covers the other half: a module installed into the
// workspace config through the overlay (Workspace.withModule, i.e. the agent's
// `install` tool) contributes its agent's tools without any export to disk —
// including its type existing in the LLM's bound schema, which the served
// (on-disk) module set alone cannot provide.
func (AgentsSuite) TestOverlayWithModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// Only editor is installed on disk; godoc arrives via the overlay.
	modGen, err := installAgents(t, c, "editor")
	require.NoError(t, err)

	out, err := modGen.
		With(daggerQuery(`{workspace: currentWorkspace{withModule(ref:"../godoc"){agents{compose{tools}}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	// Both fixtures define `shared`, so the collision namespaces both toolsets
	// — the same shape TestComposeToolset asserts for the on-disk install.
	require.Contains(t, out, "## godoc_goDoc")
	require.Contains(t, out, "## godoc_shared")
	require.Contains(t, out, "shared tool from godoc")
	require.Contains(t, out, "## editor_readFile")
	require.Contains(t, out, "## editor_shared")
}

// TestOverlayUnrelatedEditKeepsToolset is the regression guard: an overlay that
// touches nothing module-related must compose exactly the served toolset, i.e.
// the overlay re-resolution must not kick in (or duplicate anything) for
// unrelated edits.
func (AgentsSuite) TestOverlayUnrelatedEditKeepsToolset(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := installAgents(t, c, "editor", "godoc")
	require.NoError(t, err)

	baseline, err := modGen.
		With(daggerQuery(`{workspace: currentWorkspace{agents{compose{tools}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)

	out, err := modGen.
		With(daggerQuery(
			`{workspace: currentWorkspace{withNewFile(path:"/README.md", contents:%s){agents{compose{tools}}}}}`,
			strconv.Quote("unrelated overlay edit\n"),
		)).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, agentToolsFromQuery(t, baseline), agentToolsFromQuery(t, out))
}

// agentToolsFromQuery digs the composed `tools` string out of a
// `dagger query` response, whatever wrapper fields the query nested it under.
func agentToolsFromQuery(t *testctx.T, out string) string {
	t.Helper()
	var res any
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	var find func(any) (string, bool)
	find = func(v any) (string, bool) {
		obj, ok := v.(map[string]any)
		if !ok {
			return "", false
		}
		if tools, ok := obj["tools"].(string); ok {
			return tools, true
		}
		for _, child := range obj {
			if tools, ok := find(child); ok {
				return tools, true
			}
		}
		return "", false
	}
	tools, ok := find(res)
	require.True(t, ok, "no tools field in %s", out)
	return tools
}

func (AgentsSuite) TestEmptySelectionComposesBareLLM(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := installAgents(t, c, "editor")
	require.NoError(t, err)

	// A selection matching no agent folds over nothing and returns the bare
	// workspace-bound LLM (builtins only) — no error, and no editor tools.
	out, err := modGen.
		With(daggerQuery(`{workspace: currentWorkspace{agents(include:["does-not-exist"]){compose{tools}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.NotContains(t, out, "## readFile")
}
