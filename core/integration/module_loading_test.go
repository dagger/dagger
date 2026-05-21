package core

// These tests cover how Dagger chooses which module to load for a command. They
// verify path/ref resolution, precedence between candidates, and entrypoint
// selection for workspace and standalone modules.
//
// See also:
// - workspace_compat_test.go: legacy compat workspace detection.
// - workspace_selection_test.go: explicit workspace selection before loading.

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
		workdir := filepath.Join(t.TempDir(), "wórk", "sub")
		require.NoError(t, os.MkdirAll(workdir, 0o755))
		initGitRepo(ctx, t, workdir)

		copyTestdataFixture(ctx, t, filepath.Join(workdir, ".dagger", "modules", "test"), "modules", "go", "unicode-path")
		writeWorkspaceConfigFile(t, workdir, `[modules.test]
source = "modules/test"
entrypoint = true
`)

		out, err := hostDaggerExecRaw(ctx, t, workdir, "--silent", "call", "hello")
		require.NoError(t, err)
		require.Equal(t, "hello", strings.TrimSpace(string(out)))
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
			With(withModuleFixture(t, c, "submodule", "dang/relmod")).
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

	t.Run("canonical equivalent module paths dedupe to one source", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceFixture(t, c, "module-loading/canonical-source")

		out, err := ctr.With(moduleLoadingDaggerCall("message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "loaded once", strings.TrimSpace(out))
	})
}

func (ModuleLoadingSuite) TestModuleSourceAddressValidation(ctx context.Context, t *testctx.T) {
	validModule := func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithNewFile("main.dang", `
type App {
  pub hello: String! {
    "hello"
  }
}
`)
	}

	t.Run("local source cannot escape context", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := workspaceBase(t, c).
			With(validModule).
			WithNewFile("dagger.json", `{"name":"app","sdk":{"source":"dang"},"source":".."}`).
			With(moduleLoadingDaggerQueryFail(`{hello}`, "-m", ".")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `source path ".." escapes context from source root "."`)
	})

	t.Run("local source cannot be absolute", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := workspaceBase(t, c).
			With(validModule).
			WithNewFile("dagger.json", `{"name":"app","sdk":{"source":"dang"},"source":"/tmp"}`).
			With(moduleLoadingDaggerQueryFail(`{hello}`, "-m", ".")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `source path "/tmp" is absolute`)
	})

	t.Run("local dependency source cannot escape context", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := workspaceBase(t, c).
			With(validModule).
			WithNewFile("dagger.json", `{
  "name": "app",
  "sdk": {"source": "dang"},
  "dependencies": [{"name": "escape", "source": ".."}]
}`).
			With(moduleLoadingDaggerQueryFail(`{escape{hello}}`, "-m", ".")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `local module dep source path ".." escapes context "/work"`)
	})

	t.Run("local dependency source cannot be absolute", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := workspaceBase(t, c).
			With(validModule).
			WithNewFile("dagger.json", `{
  "name": "app",
  "sdk": {"source": "dang"},
  "dependencies": [{"name": "escape", "source": "/tmp/foo"}]
}`).
			With(moduleLoadingDaggerQueryFail(`{escape{hello}}`, "-m", ".")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `local module dep source path "/tmp/foo" is absolute`)
	})
}

// TestAmbientWorkspaceModuleLoading should pin down the baseline runtime shape
// of a configured workspace: one ambient entrypoint promoted to Query root,
// sibling modules still loaded under their names, and the same layout visible
// through dagger functions, dagger call, and GraphQL.
func (ModuleLoadingSuite) TestAmbientWorkspaceModuleLoading(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "module-loading/ambient")

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
		ctr := workspaceFixture(t, c, "module-loading/entrypoint-greeter").
			WithNewFile("hello.txt", "hello from workspace")

		out, err := ctr.With(daggerCall("read")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from workspace", strings.TrimSpace(out))
	})

	t.Run("workspace without ambient entrypoint keeps modules namespaced", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceFixture(t, c, "module-loading/namespaced")

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
	ctr := workspaceFixture(t, c, "module-loading/multiple-entrypoints")

	out, err := ctr.With(moduleLoadingDaggerExecFail("functions")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "multiple distinct ambient entrypoint modules")
	require.Contains(t, out, "alpha")
	require.Contains(t, out, "beta")
}

// TestModuleLoadingPrecedence should cover the explicit runtime precedence
// rules after dedupe: configured workspaces own module loading and extra
// modules win when present.
func (ModuleLoadingSuite) TestModuleLoadingPrecedence(ctx context.Context, t *testctx.T) {
	t.Run("configured workspace ignores cwd dagger.json", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithNewFile(".dagger/config.toml", `[modules.ambient]
source = "modules/ambient"
entrypoint = true
`).
			With(withModuleFixture(t, c, ".dagger/modules/ambient", "dang/ambient-build")).
			With(withModuleFixture(t, c, "nested", "dang/nested-build")).
			WithWorkdir("/work/nested")

		out, err := ctr.With(moduleLoadingDaggerCall("build")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ambient build", strings.TrimSpace(out))

		out, err = ctr.With(moduleLoadingDaggerCallFail("nested", "build")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "nested")
	})

	t.Run("extra module loads without inferring cwd dagger.json", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			With(withModuleFixture(t, c, "nested", "dang/nested-build")).
			With(withModuleFixture(t, c, "extra", "dang/extra-build")).
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
		ctr := workspaceFixture(t, c, "module-loading/ambient-build").
			With(withModuleFixture(t, c, "extra", "dang/extra-build"))

		out, err := ctr.With(moduleLoadingDaggerCall("-m", "./extra", "build")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "extra build", strings.TrimSpace(out))
	})

	t.Run("no module mode suppresses ambient module loading", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceFixture(t, c, "module-loading/ambient-build").
			With(withModuleFixture(t, c, "nested", "dang/nested-build")).
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
		ctr := workspaceFixture(t, c, "module-loading/deduped")

		out, err := ctr.With(moduleLoadingDaggerCall("message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "deduped app", strings.TrimSpace(out))
	})

	t.Run("multiple distinct extra entrypoints are rejected", func(ctx context.Context, t *testctx.T) {
		t.Skip("public CLI exposes one --mod entrypoint; multiple extra-module entrypoints are covered at the engine arbitration layer")
	})

	t.Run("multiple distinct ambient entrypoints are rejected", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceFixture(t, c, "module-loading/multiple-entrypoints")

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
		base := workspaceFixture(t, c, "module-loading/root-shadow")

		out, err := base.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "build")
		require.Contains(t, out, "container")

		out, err = base.With(daggerCall("container")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "custom container", strings.TrimSpace(out))
	})

	t.Run("constructor argument overlap", func(ctx context.Context, t *testctx.T) {
		base := workspaceFixture(t, c, "module-loading/ctor-overlap")

		out, err := base.With(daggerCall("--prefix", "ctor", "echo", "--prefix", "method")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ctor:method", strings.TrimSpace(out))

		out, err = base.With(daggerQuery(`{with(prefix:"ctor"){echo(prefix:"method")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"with":{"echo":"ctor:method"}}`, out)
	})

	t.Run("self-named method does not recurse", func(ctx context.Context, t *testctx.T) {
		base := moduleEntrypointFixture(t, c, "test", "dang/root-self-named")

		out, err := base.With(daggerCall("test")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "test-result", strings.TrimSpace(out))

		out, err = base.With(daggerCall("other")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "other-result", strings.TrimSpace(out))
	})

	t.Run("core return types still work through entrypoint proxies", func(ctx context.Context, t *testctx.T) {
		base := moduleEntrypointFixture(t, c, "dirs", "dang/core-types")

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
		base := moduleEntrypointFixture(t, c, "playground", "go/playground-directory-field")

		out, err := base.With(daggerQuery(`{sayHello, directory{entries}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"sayHello":"hello!", "directory":{"entries": []}}`, out)
	})
}
