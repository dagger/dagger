package core

// Workspace alignment: aligned; helper-only file for workspace-era fixtures.
// Scope: Shared workspace test fixtures, host-side workspace helpers, and container setup for workspace-focused suites.
// Intent: Keep workspace setup centralized and explicit so workspace suites do not depend on historical module helpers.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func daggerWorkspaceExec(args ...string) dagger.WithContainerFunc {
	return daggerExecRaw(append([]string{"workspace"}, args...)...)
}

func daggerWorkspaceInstall(args ...string) dagger.WithContainerFunc {
	return daggerExecRaw(append([]string{"install"}, args...)...)
}

func hostDaggerWorkspaceExec(ctx context.Context, t testing.TB, workdir string, args ...string) ([]byte, error) {
	t.Helper()
	return hostDaggerExecRaw(ctx, t, workdir, append([]string{"workspace"}, args...)...)
}

// workspaceBase returns a container with git, the dagger CLI, and an
// initialized git repo at /work — the starting point for workspace tests.
func workspaceBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "dagger@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Dagger Tests"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"})
}

// legacyWorkspaceBase creates a native git repo rooted at /work but seeds it
// with a legacy dagger.json project shape. Compat detection and migration tests
// use this to separate "legacy on disk" from "workspace at runtime".
func legacyWorkspaceBase(t testing.TB, c *dagger.Client, config string, ops ...dagger.WithContainerFunc) *dagger.Container {
	t.Helper()

	ctr := workspaceBase(t, c).
		WithNewFile("dagger.json", config)
	for _, op := range ops {
		ctr = ctr.With(op)
	}

	return ctr.
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})
}

func ensureWorkspaceInit() dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithExec([]string{"sh", "-c", "test -f .dagger/config.toml || dagger workspace init"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

// initDangModule creates a config-owned Dang workspace module with the given
// name and source code.
func initDangModule(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(ensureWorkspaceInit()).
			With(daggerModuleExec("init", "--sdk=dang", "--name="+name)).
			WithNewFile(".dagger/modules/"+name+"/main.dang", source)
	}
}

// initStandaloneDangModule creates a standalone Dang module in the current
// working directory and overwrites main.dang with the provided source.
func initStandaloneDangModule(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(daggerModuleExec("init", "--sdk=dang", "--source=.", "--name="+name)).
			WithNewFile("main.dang", source)
	}
}

// initDangBlueprint creates a config-owned Dang workspace module and marks it
// as the workspace entrypoint so its methods are promoted to the root.
func initDangBlueprint(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(ensureWorkspaceInit()).
			With(daggerModuleExec("init", "--sdk=dang", "--name="+name)).
			WithNewFile(".dagger/modules/"+name+"/main.dang", source).
			With(daggerWorkspaceExec("config", "modules."+name+".entrypoint", "true"))
	}
}

// initHostDangBlueprint creates a minimal explicit workspace on the host with a
// single Dang entrypoint module. Host-side command tests can use this to avoid
// implicit CWD-module behavior and exercise ambient workspace loading directly.
func initHostDangBlueprint(ctx context.Context, t testing.TB, workdir, name, source string) {
	t.Helper()

	_, err := hostDaggerWorkspaceExec(ctx, t, workdir, "init")
	require.NoError(t, err)

	_, err = hostDaggerModuleExec(ctx, t, workdir, "init", "--sdk=dang", "--name="+name)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(workdir, ".dagger", "modules", name, "main.dang"), []byte(source), 0o644))

	_, err = hostDaggerWorkspaceExec(ctx, t, workdir, "config", "modules."+name+".entrypoint", "true")
	require.NoError(t, err)
}

// TestEntrypointWithFieldHidden verifies that the synthetic `with` field
// installed on Query for entrypoint constructors with arguments is hidden
// from user-facing CLI listings (`dagger functions`, `dagger call --help`)
// while remaining callable and introspectable.
func (WorkspaceSuite) TestEntrypointWithFieldHidden(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		With(initDangBlueprint("greeter", `
type Greeter {
  pub msg: String!

  new(name: String!) {
    self.msg = "hello, " + name + "!"
    self
  }

  pub greet: String! {
    msg
  }
}
`))

	t.Run("dagger functions omits with", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		// The blueprint's real functions should appear.
		require.Contains(t, out, "greet")
		// The synthetic `with` field must not leak into user listings.
		require.NotRegexp(t, `(?m)^with\b`, out)
	})

	t.Run("dagger call routes constructor args through with", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("--name=world", "greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello, world!", strings.TrimSpace(out))
	})

	t.Run("with remains in graphql introspection", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{ __type(name: "Query") { fields { name } } }`)).Stdout(ctx)
		require.NoError(t, err)
		// `with` is callable via raw GraphQL; only user-facing CLI
		// listings filter it.
		require.Contains(t, out, `"name": "with"`)
	})
}
