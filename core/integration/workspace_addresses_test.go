package core

// These tests cover Workspace.addresses: discovery of module functions loadable
// as bare "module:function" address references (hack/designs/sandboxes.md §5).
// They verify the caller-arg rule (engine-supplied args are exempt: an
// auto-injected Workspace, an @agent's base LLM), the type and directive
// filters and how they combine, entrypoint-module exclusion, kebab-case value
// rendering, and that a rendered value round-trips through Query.address.
//
// See also:
// - workspace_modules_test.go: installing and listing workspace modules.
// - agents_test.go: sibling rollup APIs over the same module set.

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// WorkspaceAddressesSuite owns address discovery over workspace-installed
// modules: which functions are listed, how values render, and that listed
// values resolve.
type WorkspaceAddressesSuite struct{}

func TestWorkspaceAddresses(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceAddressesSuite{})
}

const workspaceAddressesSandboxesSource = `
type Sandboxes {
  """Go development sandbox."""
  go: Container! {
    container.from("` + alpineImage + `")
  }

  """Workspace-aware sandbox; the Workspace arg is auto-injected."""
  wsAware(ws: Workspace!): Container! {
    container.from("` + alpineImage + `")
  }

  plain: Container! {
    container.from("` + alpineImage + `")
  }

  """Requires an image argument; not addressable."""
  custom(image: String!): Container! {
    container.from(image)
  }

  """A Directory, not a Container."""
  data: Directory! {
    directory.withNewFile("hello.txt", "hi")
  }

  """Not an object at all."""
  greet: String! {
    "hello"
  }

  """Coding agent; the base LLM is engine-supplied."""
  coder(base: LLM!): LLM! @agent {
    base.withTools(currentNode).withSystemPrompt("coder agent system prompt")
  }

  """Lints the workspace; a check that happens to return a Container."""
  lint: Container! @check {
    container.from("` + alpineImage + `")
  }
}
`

const workspaceAddressesEntrySource = `
type Entry {
  """Hoisted onto the root; not addressable as entry:entry-ctr."""
  entryCtr: Container! {
    container.from("` + alpineImage + `")
  }
}
`

func (WorkspaceAddressesSuite) TestWorkspaceAddresses(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("dagger.toml", `[modules.sandboxes]
source = ".dagger/modules/sandboxes"

[modules.entry]
source = ".dagger/modules/entry"
entrypoint = true
`).
		WithNewFile(".dagger/modules/sandboxes/dagger.json", `{"name":"sandboxes","engineVersion":"v1.0.0","sdk":{"source":"dang"}}`).
		WithNewFile(".dagger/modules/sandboxes/main.dang", workspaceAddressesSandboxesSource).
		WithNewFile(".dagger/modules/entry/dagger.json", `{"name":"entry","engineVersion":"v1.0.0","sdk":{"source":"dang"}}`).
		WithNewFile(".dagger/modules/entry/main.dang", workspaceAddressesEntrySource)

	t.Run("container addresses", func(ctx context.Context, t *testctx.T) {
		// go, lint and plain appear (zero args); wsAware appears (its only
		// required arg is an auto-injected Workspace); custom is dropped
		// (required String arg); data, greet and coder are dropped (wrong
		// return type); entry's entryCtr is dropped (entrypoint modules are
		// hoisted, not addressable). Values are kebab-case and sorted; plain
		// has no docstring, so its description is empty.
		out, err := base.
			With(daggerQuery(`{currentWorkspace{addresses(types:["Container"]){value description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:go","description":"Go development sandbox."},
			{"value":"sandboxes:lint","description":"Lints the workspace; a check that happens to return a Container."},
			{"value":"sandboxes:plain","description":""},
			{"value":"sandboxes:ws-aware","description":"Workspace-aware sandbox; the Workspace arg is auto-injected."}
		]}}`, out)
	})

	t.Run("directory addresses", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerQuery(`{currentWorkspace{addresses(types:["Directory"]){value description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:data","description":"A Directory, not a Container."}
		]}}`, out)
	})

	t.Run("llm addresses", func(ctx context.Context, t *testctx.T) {
		// coder's only required arg is its @agent base, which the engine
		// supplies the same way it supplies a Workspace, so the agent is
		// addressable.
		out, err := base.
			With(daggerQuery(`{currentWorkspace{addresses(types:["LLM"]){value description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:coder","description":"Coding agent; the base LLM is engine-supplied."}
		]}}`, out)
	})

	t.Run("interface addresses", func(ctx context.Context, t *testctx.T) {
		// An interface name lists functions returning any implementor, with
		// the concrete type alongside so the caller can pick a loader.
		// Container and Directory are Exportable; LLM is not.
		out, err := base.
			With(daggerQuery(`{currentWorkspace{addresses(types:["Exportable"]){value type}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:data","type":"Directory"},
			{"value":"sandboxes:go","type":"Container"},
			{"value":"sandboxes:lint","type":"Container"},
			{"value":"sandboxes:plain","type":"Container"},
			{"value":"sandboxes:ws-aware","type":"Container"}
		]}}`, out)

		// All three are Syncers; the caller-arg rule still applies (custom is
		// a Container but needs an argument) and so does the object rule
		// (greet is a String).
		out, err = base.
			With(daggerQuery(`{currentWorkspace{addresses(types:["Syncer"]){value type}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:coder","type":"LLM"},
			{"value":"sandboxes:data","type":"Directory"},
			{"value":"sandboxes:go","type":"Container"},
			{"value":"sandboxes:lint","type":"Container"},
			{"value":"sandboxes:plain","type":"Container"},
			{"value":"sandboxes:ws-aware","type":"Container"}
		]}}`, out)
	})

	t.Run("type list matches any entry once", func(ctx context.Context, t *testctx.T) {
		// A list is a disjunction: Container or Directory. Overlapping
		// entries (every Container is a Syncer) still list each function once,
		// which is what stitching separate calls together cannot give.
		out, err := base.
			With(daggerQuery(`{currentWorkspace{addresses(types:["Container","Directory"]){value}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:data"},
			{"value":"sandboxes:go"},
			{"value":"sandboxes:lint"},
			{"value":"sandboxes:plain"},
			{"value":"sandboxes:ws-aware"}
		]}}`, out)

		out, err = base.
			With(daggerQuery(`{currentWorkspace{addresses(types:["Container","Syncer"]){value}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:coder"},
			{"value":"sandboxes:data"},
			{"value":"sandboxes:go"},
			{"value":"sandboxes:lint"},
			{"value":"sandboxes:plain"},
			{"value":"sandboxes:ws-aware"}
		]}}`, out)
	})

	t.Run("directive addresses", func(ctx context.Context, t *testctx.T) {
		// A directive name lists functions whose field carries it, as the
		// schema renders it; each address reports its directives by name.
		out, err := base.
			With(daggerQuery(`{currentWorkspace{addresses(directives:["agent"]){value type directives}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"sandboxes:coder"`)
		require.NotContains(t, out, `"sandboxes:lint"`)
		require.Contains(t, out, `"agent"`)

		// The directive list is a disjunction too.
		out, err = base.
			With(daggerQuery(`{currentWorkspace{addresses(directives:["check","agent"]){value}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:coder"},
			{"value":"sandboxes:lint"}
		]}}`, out)

		// The two filters conjoin: no Container is an @agent.
		out, err = base.
			With(daggerQuery(`{currentWorkspace{addresses(types:["Container"],directives:["agent"]){value}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[]}}`, out)

		out, err = base.
			With(daggerQuery(`{currentWorkspace{addresses(types:["Container"],directives:["check"]){value}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[{"value":"sandboxes:lint"}]}}`, out)
	})

	t.Run("null and empty lists", func(ctx context.Context, t *testctx.T) {
		// No filter lists every addressable function.
		out, err := base.
			With(daggerQuery(`{currentWorkspace{addresses{value}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:coder"},
			{"value":"sandboxes:data"},
			{"value":"sandboxes:go"},
			{"value":"sandboxes:lint"},
			{"value":"sandboxes:plain"},
			{"value":"sandboxes:ws-aware"}
		]}}`, out)

		// An empty list is an empty disjunction: nothing matches.
		out, err = base.
			With(daggerQuery(`{currentWorkspace{addresses(types:[]){value}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[]}}`, out)

		out, err = base.
			With(daggerQuery(`{currentWorkspace{addresses(directives:[]){value}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[]}}`, out)
	})

	t.Run("no matches", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerQuery(`{currentWorkspace{addresses(types:["Service"]){value}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[]}}`, out)
	})

	t.Run("unknown names are errors", func(ctx context.Context, t *testctx.T) {
		// Names are exact identifiers, so a typo errors against the served
		// schema rather than silently matching nothing.
		out, err := base.
			With(daggerQueryFail(`{currentWorkspace{addresses(types:["Containr"]){value}}}`)).
			Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `unknown object or interface type "Containr"`)

		out, err = base.
			With(daggerQueryFail(`{currentWorkspace{addresses(directives:["chekc"]){value}}}`)).
			Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `unknown directive "chekc"`)

		// Scalars are declared types but nothing loadable as an address.
		out, err = base.
			With(daggerQueryFail(`{currentWorkspace{addresses(types:["String"]){value}}}`)).
			Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `unknown object or interface type "String"`)
	})

	t.Run("listed value resolves through Query.address", func(ctx context.Context, t *testctx.T) {
		// The rendered kebab-case value must be loadable by the address
		// machinery the discovery exists for (resolveModuleRef normalizes
		// both segments, so ws-aware resolves to wsAware).
		out, err := base.
			With(daggerQuery(`{address(value:"sandboxes:ws-aware"){container{platform}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "platform")
	})

	t.Run("agent address resolves through Query.address", func(ctx context.Context, t *testctx.T) {
		// A bare agent reference resolves to the agent composed onto the same
		// seed Workspace.agents.compose uses with no base: a fresh LLM bound to
		// the workspace in scope. The Sandboxes tools prove the middleware ran
		// on that seed; the workspace's entries prove the seed is bound.
		out, err := base.
			With(daggerQuery(`{address(value:"sandboxes:coder"){llm{tools workspace{directory(path:"/"){entries}}}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "## custom")
		require.Contains(t, out, "dagger.toml")
	})
}
