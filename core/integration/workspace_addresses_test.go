package core

// These tests cover Workspace.addresses: discovery of module functions loadable
// as bare "module:function" address references (hack/designs/sandboxes.md §5).
// They verify return-type filtering, the caller-arg rule (engine-supplied args
// are exempt: an auto-injected Workspace, an @agent's base LLM),
// entrypoint-module exclusion, kebab-case value rendering, and that a rendered
// value round-trips through Query.address.
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
		// go and plain appear (zero args); wsAware appears (its only required
		// arg is an auto-injected Workspace); custom is dropped (required
		// String arg); data, greet and coder are dropped (wrong return type);
		// entry's entryCtr is dropped (entrypoint modules are hoisted, not
		// addressable). Values are kebab-case and sorted; plain has no
		// docstring, so its description is empty.
		out, err := base.
			With(daggerQuery(`{currentWorkspace{addresses(type:"Container"){value description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:go","description":"Go development sandbox."},
			{"value":"sandboxes:plain","description":""},
			{"value":"sandboxes:ws-aware","description":"Workspace-aware sandbox; the Workspace arg is auto-injected."}
		]}}`, out)
	})

	t.Run("directory addresses", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerQuery(`{currentWorkspace{addresses(type:"Directory"){value description}}}`)).
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
			With(daggerQuery(`{currentWorkspace{addresses(type:"LLM"){value description}}}`)).
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
			With(daggerQuery(`{currentWorkspace{addresses(type:"Exportable"){value type}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:data","type":"Directory"},
			{"value":"sandboxes:go","type":"Container"},
			{"value":"sandboxes:plain","type":"Container"},
			{"value":"sandboxes:ws-aware","type":"Container"}
		]}}`, out)

		// All three are Syncers; the caller-arg rule still applies (custom is
		// a Container but needs an argument) and so does the object rule
		// (greet is a String).
		out, err = base.
			With(daggerQuery(`{currentWorkspace{addresses(type:"Syncer"){value type}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[
			{"value":"sandboxes:coder","type":"LLM"},
			{"value":"sandboxes:data","type":"Directory"},
			{"value":"sandboxes:go","type":"Container"},
			{"value":"sandboxes:plain","type":"Container"},
			{"value":"sandboxes:ws-aware","type":"Container"}
		]}}`, out)
	})

	t.Run("no matches", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerQuery(`{currentWorkspace{addresses(type:"Service"){value}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"addresses":[]}}`, out)
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
