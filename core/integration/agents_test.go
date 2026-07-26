package core

// These tests cover `dagger agent`, which discovers and composes module @agent
// middlewares (hack/designs/workspace-agents.md §3). They verify cross-module discovery, the base
// argument being matched by type (not name), nested discovery through
// object-returning functions, signature validation, and the composed toolset
// (auto-exclusion of the entrypoint + collision last-wins). Driving the
// interactive prompt itself needs a live model and is covered by manual QA.
//
// See also:
// - checks_test.go: the @check sibling this machinery is cloned from.
// - workspace_modules_test.go: installing modules into workspaces.

import (
	"context"
	"fmt"
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
	// Tools from both modules fold onto one LLM (godoc's tool proves the
	// `llm`-named base was threaded correctly). Names chosen to not be a
	// substring of a builtin (e.g. `read` ⊂ `read_skill`).
	require.Contains(t, out, "## readFile")
	require.Contains(t, out, "## goDoc")
	// The @agent entrypoint is auto-excluded from the toolset, so authors don't
	// need `except: ["agent"]`.
	require.NotContains(t, out, "## agent")
	// Collision: godoc:agent folds after editor:agent (alphabetical module:fn
	// order), so godoc's `shared` wins (last withTools wins).
	require.Contains(t, out, "shared tool from godoc")
	require.NotContains(t, out, "shared tool from editor")
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
