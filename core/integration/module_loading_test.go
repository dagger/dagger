package core

// Scope: Module source resolution, nomination, precedence, and entrypoint arbitration for native workspace and module sources.
// Intent: Keep loading behavior separate from compat detection and make source-resolution and arbitration ownership explicit.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// ModuleLoadingSuite owns native module loading behavior from source resolution
// through entrypoint arbitration.
//
// Compat workspace detection from legacy dagger.json belongs in
// workspace_compat_test.go. This file may cover loading edge cases without a
// workspace, but it should not own compat detection itself.
type ModuleLoadingSuite struct{}

func TestModuleLoading(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleLoadingSuite{})
}

func moduleLoadingDaggerExec(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func moduleLoadingDaggerExecFail(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func moduleLoadingDaggerCall(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "call"}, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func moduleLoadingDaggerCallFail(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "call"}, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func moduleLoadingDaggerFunctions(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "functions"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func moduleLoadingDaggerQuery(query string, args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "query"}, args...), dagger.ContainerWithExecOpts{
			Stdin:                         query,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func moduleLoadingDaggerQueryFail(query string, args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "query"}, args...), dagger.ContainerWithExecOpts{
			Stdin:                         query,
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func moduleLoadingDangModule(dir, name, typeName, fnName, result string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithNewFile(dir+"/dagger.json", `{"name":"`+name+`","sdk":{"source":"dang"}}`).
			WithNewFile(dir+"/main.dang", `
type `+typeName+` {
  pub `+fnName+`: String! {
    "`+result+`"
  }
}
`)
	}
}

// TestModuleSourceResolution should pin down how module loading behaves before
// arbitration, including the cases where a path does not actually resolve to a
// module source.
func (ModuleLoadingSuite) TestModuleSourceResolution(ctx context.Context, t *testctx.T) {
	t.Run("context directory is empty when path has no module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "foo")
		require.NoError(t, os.WriteFile(filePath, []byte("foo"), 0o644))

		ents, err := c.ModuleSource(tmpDir).ContextDirectory().Entries(ctx)
		require.NoError(t, err)
		require.Empty(t, ents)
	})

	t.Run("module under unicode path initializes and loads", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/wórk/sub/").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/wórk/sub/main.go", `package main
 			import (
 				"context"
 			)
 			type Test struct {}
 			func (m *Test) Hello(ctx context.Context) string {
				return "hello"
 			}
 			`,
			).
			With(daggerQuery(`{hello}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hello":"hello"}`, out)
	})

	t.Run("source may point to ancestor within context", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := goGitBase(t, c).
			WithNewFile("source/main.dang", `
type App {
  pub hello: String! {
    "hello from ancestor source"
  }
}
`).
			WithNewFile("configs/app/dagger.json", `{
  "name": "app",
  "sdk": {"source": "dang"},
  "source": "../../source"
}`)

		out, err := ctr.With(daggerCallAt("configs/app", "hello")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from ancestor source", strings.TrimSpace(out))
	})

	t.Run("relative extra module path resolves from invocation cwd", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		base := workspaceBase(t, c).
			With(moduleLoadingDangModule("submodule", "relmod", "Relmod", "whoami", "loaded from submodule")).
			WithNewFile("nested/.keep", "")

		for _, tc := range []struct {
			name    string
			workdir string
			modRef  string
		}{
			{name: "workspace root", workdir: "/work", modRef: "./submodule"},
			{name: "nested cwd", workdir: "/work/nested", modRef: "../submodule"},
		} {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				out, err := base.
					WithWorkdir(tc.workdir).
					With(moduleLoadingDaggerCall("-m", tc.modRef, "whoami")).
					Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "loaded from submodule", strings.TrimSpace(out))
			})
		}
	})

	t.Run("missing module path fails clearly", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := workspaceBase(t, c).
			With(moduleLoadingDaggerCallFail("-m", "./missing", "hello")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "missing")
		require.Contains(t, strings.ToLower(out), "not")
	})

	t.Run("non-directory module path fails clearly", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := workspaceBase(t, c).
			WithNewFile("not-a-dir", "this is a file").
			With(moduleLoadingDaggerCallFail("-m", "./not-a-dir", "hello")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "not-a-dir")
		require.Contains(t, strings.ToLower(out), "director")
	})

	t.Run("canonical and symlinked module paths dedupe to one source", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(moduleLoadingDangModule("modules/app", "app", "App", "message", "loaded once")).
			WithExec([]string{"ln", "-s", "modules/app", "linked-app"}).
			WithNewFile(".dagger/config.toml", `[modules.real]
source = "modules/app"
entrypoint = true

[modules.linked]
source = "linked-app"
entrypoint = true
`)

		out, err := ctr.With(moduleLoadingDaggerCall("message")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Equal(t, "loaded once", strings.TrimSpace(out))
	})
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

	t.Run("workspace without ambient entrypoint keeps modules namespaced", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			With(initDangModule("build", `
type Build {
  pub run: String! {
    "build ran"
  }
}
`)).
			With(initDangModule("lint", `
type Lint {
  pub run: String! {
    "lint ran"
  }
}
`))

		out, err := ctr.With(moduleLoadingDaggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "build")
		require.Contains(t, out, "lint")

		out, err = ctr.With(moduleLoadingDaggerCall("build", "run")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "build ran", strings.TrimSpace(out))

		out, err = ctr.With(moduleLoadingDaggerCallFail("run")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "run")
	})
}

// TestAmbientWorkspaceValidation should lock down invalid ambient workspace
// configurations before any runtime loading occurs.
func (ModuleLoadingSuite) TestAmbientWorkspaceValidation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := workspaceBase(t, c).
		With(initDangModule("alpha", `
type Alpha {
  pub run: String! {
    "alpha"
  }
}
`)).
		With(initDangModule("beta", `
type Beta {
  pub run: String! {
    "beta"
  }
}
`)).
		With(daggerWorkspaceExec("config", "modules.alpha.entrypoint", "true")).
		With(daggerWorkspaceExec("config", "modules.beta.entrypoint", "true"))

	out, err := ctr.With(moduleLoadingDaggerExecFail("functions")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "multiple distinct ambient entrypoint modules")
	require.Contains(t, out, "alpha")
	require.Contains(t, out, "beta")
}

// TestModuleLoadingPrecedence should cover the explicit runtime precedence
// rules after dedupe: extra modules > CWD module > ambient workspace modules.
func (ModuleLoadingSuite) TestModuleLoadingPrecedence(ctx context.Context, t *testctx.T) {
	t.Run("cwd module overrides ambient entrypoint", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(initDangBlueprint("ambient", `
type Ambient {
  pub build: String! {
    "ambient build"
  }
}
`)).
			With(moduleLoadingDangModule("nested", "nested", "Nested", "build", "cwd build")).
			WithWorkdir("/work/nested")

		out, err := ctr.With(moduleLoadingDaggerCall("build")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "cwd build", strings.TrimSpace(out))

		out, err = ctr.With(moduleLoadingDaggerCall("ambient", "build")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ambient build", strings.TrimSpace(out))
	})

	t.Run("extra module suppresses cwd module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(moduleLoadingDangModule("nested", "nested", "Nested", "build", "cwd build")).
			With(moduleLoadingDangModule("extra", "extra", "Extra", "build", "extra build")).
			WithWorkdir("/work/nested")

		out, err := ctr.With(moduleLoadingDaggerCall("-m", "../extra", "build")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "extra build", strings.TrimSpace(out))

		out, err = ctr.With(moduleLoadingDaggerCallFail("-m", "../extra", "nested", "build")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "nested")
	})

	t.Run("extra modules override ambient workspace entrypoint", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(initDangBlueprint("ambient", `
type Ambient {
  pub build: String! {
    "ambient build"
  }
}
`)).
			With(moduleLoadingDangModule("extra", "extra", "Extra", "build", "extra build"))

		out, err := ctr.With(moduleLoadingDaggerCall("-m", "./extra", "build")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "extra build", strings.TrimSpace(out))
	})

	t.Run("no module mode suppresses ambient and cwd module loading", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(initDangBlueprint("ambient", `
type Ambient {
  pub build: String! {
    "ambient build"
  }
}
`)).
			With(moduleLoadingDangModule("nested", "nested", "Nested", "build", "cwd build")).
			WithWorkdir("/work/nested")

		out, err := ctr.With(moduleLoadingDaggerQuery(`{version}`, "-M")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"version"`)

		out, err = ctr.With(moduleLoadingDaggerQueryFail(`{build}`, "-M")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "build")
	})
}

// TestModuleLoadingDedupeAndConflicts should cover generic module dedupe and
// the same-tier conflict errors introduced by entrypoint arbitration.
func (ModuleLoadingSuite) TestModuleLoadingDedupeAndConflicts(ctx context.Context, t *testctx.T) {
	t.Run("duplicate nominations are deduped before arbitration", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(moduleLoadingDangModule("modules/app", "app", "App", "message", "deduped app")).
			WithNewFile(".dagger/config.toml", `[modules.first]
source = "modules/app"
entrypoint = true

[modules.second]
source = "modules/app"
entrypoint = true
`)

		out, err := ctr.With(moduleLoadingDaggerCall("message")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Equal(t, "deduped app", strings.TrimSpace(out))
	})

	t.Run("multiple distinct extra entrypoints are rejected", func(ctx context.Context, t *testctx.T) {
		t.Skip("public CLI exposes one --mod entrypoint; multiple extra-module entrypoints are covered at the engine arbitration layer")
	})

	t.Run("multiple distinct ambient entrypoints are rejected", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(initDangModule("alpha", `
type Alpha {
  pub run: String! {
    "alpha"
  }
}
`)).
			With(initDangModule("beta", `
type Beta {
  pub run: String! {
    "beta"
  }
}
`)).
			With(daggerWorkspaceExec("config", "modules.alpha.entrypoint", "true")).
			With(daggerWorkspaceExec("config", "modules.beta.entrypoint", "true"))

		out, err := ctr.With(moduleLoadingDaggerExecFail("functions")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "multiple distinct ambient entrypoint modules")
		require.Contains(t, out, "alpha")
		require.Contains(t, out, "beta")
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
