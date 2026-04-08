package core

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// ModuleLoadingSuite owns runtime module loading from every nomination path:
// workspace config, CWD module, -m, and extra modules. This file is about
// what actually loads and which module wins as the active entrypoint.
type ModuleLoadingSuite struct{}

func TestModuleLoading(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleLoadingSuite{})
}

// TestAmbientWorkspaceModuleLoading should pin down the baseline runtime shape
// of a configured workspace: one ambient entrypoint promoted to Query root,
// sibling modules still loaded under their names, and the same layout visible
// through dagger functions, dagger call, and GraphQL.
func (ModuleLoadingSuite) TestAmbientWorkspaceModuleLoading(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		With(initDangBlueprint("ci", `
type Ci {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub build: String! {
    "built!"
  }

  pub deploy: String! {
    "deployed!"
  }
}
`)).
		With(initDangModule("lint", `
type Lint {
  pub check: String! {
    "lint passed"
  }
}
`)).
		With(initDangModule("test", `
type Test {
  pub run: String! {
    "tests passed"
  }
}
`))

	t.Run("dagger functions shows all modules", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "build")
		require.Contains(t, out, "deploy")
		require.Contains(t, out, "lint")
		require.Contains(t, out, "test")
	})

	t.Run("dagger call entrypoint function", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("build")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "built!", strings.TrimSpace(out))
	})

	t.Run("dagger call sibling module function", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("lint", "check")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "lint passed", strings.TrimSpace(out))
	})

	t.Run("query root exposes entrypoint methods", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{build,lint{check},test{run}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"build":"built!","lint":{"check":"lint passed"},"test":{"run":"tests passed"}}`, out)
	})

	t.Run("entrypoint module with workspace arg", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello from workspace").
			With(initDangBlueprint("greeter", `
type Greeter {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub read: String! {
    source.file("hello.txt").contents
  }
}
`))

		out, err := ctr.With(daggerCall("read")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from workspace", strings.TrimSpace(out))
	})
}

// TestAmbientWorkspaceValidation should lock down invalid ambient workspace
// configurations before any runtime loading occurs.
func (ModuleLoadingSuite) TestAmbientWorkspaceValidation(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement ambient workspace validation coverage.

Create an invalid workspace config, for example with multiple distinct ambient
entrypoint modules, and verify workspace load fails with a clear validation
error instead of serving an ambiguous Query root.`)
}

// TestModuleLoadingPrecedence should cover the explicit runtime precedence
// rules after dedupe: extra modules > CWD module > ambient workspace modules.
func (ModuleLoadingSuite) TestModuleLoadingPrecedence(ctx context.Context, t *testctx.T) {
	t.Run("cwd module overrides ambient entrypoint", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement CWD-vs-ambient precedence coverage.

Invoke Dagger from inside a nested module directory under an initialized
workspace. Verify the nested CWD module becomes the active entrypoint while the
ambient workspace remains loaded as context.`)
	})

	t.Run("extra module suppresses cwd module", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement extra-module-vs-CWD precedence coverage.

Invoke Dagger with -m from inside a nested module directory. Verify the extra
module becomes the active entrypoint and the CWD module is not loaded as a
second entrypoint.`)
	})

	t.Run("extra modules override ambient workspace entrypoint", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement extra-vs-ambient precedence coverage.

Nominate an ambient workspace entrypoint and a distinct extra module in the
same invocation. Verify the extra module wins as the active entrypoint.`)
	})
}

// TestModuleLoadingDedupeAndConflicts should cover generic module dedupe and
// the same-tier conflict errors introduced by entrypoint arbitration.
func (ModuleLoadingSuite) TestModuleLoadingDedupeAndConflicts(ctx context.Context, t *testctx.T) {
	t.Run("duplicate nominations are deduped before arbitration", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement duplicate nomination dedupe coverage.

Nominate the same module through more than one path, for example via workspace
config and -m, and verify it is loaded once before entrypoint arbitration
runs.`)
	})

	t.Run("multiple distinct extra entrypoints are rejected", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement same-tier extra entrypoint conflict coverage.

Request more than one distinct extra module as an entrypoint candidate and
verify the runtime rejects the invocation with a clear error.`)
	})

	t.Run("multiple distinct ambient entrypoints are rejected", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement same-tier ambient entrypoint conflict coverage.

Serve a workspace config that nominates more than one distinct ambient
entrypoint and verify load fails with a clear error.`)
	})
}

// TestEntryPointRootRouting should cover the root-level routing and shadowing
// edge cases once an entrypoint module wins arbitration.
func (ModuleLoadingSuite) TestEntrypointRootRouting(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("root field shadowing", func(ctx context.Context, t *testctx.T) {
		base := workspaceBase(t, c).
			With(initDangBlueprint("ci", `
type Ci {
  pub build: String! {
    "built!"
  }

  pub container: String! {
    "custom container"
  }
}
`))

		out, err := base.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "build")
		require.Contains(t, out, "container")

		out, err = base.With(daggerCall("container")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "custom container", strings.TrimSpace(out))
	})

	t.Run("constructor argument overlap", func(ctx context.Context, t *testctx.T) {
		base := workspaceBase(t, c).
			With(initDangBlueprint("ci", `
type Ci {
  pub prefix: String!

  new(prefix: String! = "ctor") {
    self.prefix = prefix
    self
  }

  pub echo(prefix: String! = "method"): String! {
    self.prefix + ":" + prefix
  }
}
`))

		out, err := base.With(daggerCall("--prefix", "ctor", "echo", "--prefix", "method")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ctor:method", strings.TrimSpace(out))

		out, err = base.With(daggerQuery(`{with(prefix:"ctor"){echo(prefix:"method")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"with":{"echo":"ctor:method"}}`, out)
	})

	t.Run("self-named method does not recurse", func(ctx context.Context, t *testctx.T) {
		base := workspaceBase(t, c).
			With(initStandaloneDangModule("test", `
type Test {
  pub test: String! {
    "test-result"
  }

  pub other: String! {
    "other-result"
  }
}
`))

		out, err := base.With(daggerCall("test")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "test-result", strings.TrimSpace(out))

		out, err = base.With(daggerCall("other")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "other-result", strings.TrimSpace(out))
	})

	t.Run("core return types still work through entrypoint proxies", func(ctx context.Context, t *testctx.T) {
		base := workspaceBase(t, c).
			With(initStandaloneDangModule("dirs", `
type Dirs {
  pub directory: Directory! {
    Dagger.directory.withNewFile("hello.txt", "hello from dirs")
  }

  pub file: File! {
    Dagger.file("greeting.txt", "hi")
  }

  pub container: Container! {
    Dagger.container.from("alpine:3.20")
  }

  pub greet: String! {
    "greetings!"
  }
}
`))

		out, err := base.With(daggerCall("greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "greetings!", strings.TrimSpace(out))

		out, err = base.With(daggerCall("directory", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello.txt", strings.TrimSpace(out))

		out, err = base.With(daggerCall("file", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi", strings.TrimSpace(out))

		out, err = base.With(daggerCall("container", "with-exec", "--args=cat,/etc/alpine-release", "stdout")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, strings.TrimSpace(out), "3.20")
	})

	t.Run("directory field does not recurse in container runtime plumbing", func(ctx context.Context, t *testctx.T) {
		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("module", "init", "--name=playground", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"dagger/playground/internal/dagger"
)

type Playground struct {
	*dagger.Directory
}

func New() Playground {
	return Playground{Directory: dag.Directory()}
}

func (p *Playground) SayHello() string {
	return "hello!"
}
`)

		out, err := base.With(daggerQuery(`{sayHello, directory{entries}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"sayHello":"hello!", "directory":{"entries": []}}`, out)
	})
}
