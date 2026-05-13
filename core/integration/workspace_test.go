package core

// This file contains shared workspace fixtures, host-side helpers, and
// container setup for workspace-focused tests. It should not own behavior
// coverage directly.

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

// gitRepoBase returns a container with git, the dagger CLI, and an
// initialized git repo at /work
func gitRepoBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "dagger@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Dagger Tests"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"})
}

// workspaceBase returns a git-backed /work with the CLI installed, but no native
// .dagger/config.toml. A git root enables workspace/lockfile detection; a
// native config opts into native workspace behavior and suppresses legacy
// dagger.json compat inference, so tests should add it explicitly when needed.
func workspaceBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return gitRepoBase(t, c)
}

// nativeWorkspaceBase adds the native workspace state created by
// `dagger workspace init`: a .dagger/config.toml inside the git root.
func nativeWorkspaceBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return workspaceBase(t, c).With(daggerExec("workspace", "init"))
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
		return ctr.WithExec([]string{"sh", "-c", "dagger workspace config-file | grep -vx none >/dev/null || dagger workspace init --here"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

// initDangModule creates a config-owned Dang workspace module with the given
// name and source code.
func initDangModule(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(daggerExec("module", "init", "--here", "--sdk=dang", name)).
			WithNewFile(".dagger/modules/"+name+"/main.dang", source)
	}
}

// initStandaloneDangModule creates a standalone Dang module in the current
// working directory and overwrites main.dang with the provided source.
func initStandaloneDangModule(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(daggerExec("module", "init", "--sdk=dang", "--source=.", name, ".")).
			WithNewFile("main.dang", source)
	}
}

// initDangBlueprint creates a config-owned Dang workspace module and marks it
// as the workspace entrypoint so its methods are promoted to the root.
func initDangBlueprint(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(daggerExec("module", "init", "--here", "--sdk=dang", name)).
			WithNewFile(".dagger/modules/"+name+"/main.dang", source).
			With(daggerExec("workspace", "config", "modules."+name+".entrypoint", "true"))
	}
}

// initHostDangBlueprint creates a minimal explicit workspace on the host with a
// single Dang entrypoint module. Host-side command tests can use this to avoid
// implicit CWD-module behavior and exercise ambient workspace loading directly.
func initHostDangBlueprint(ctx context.Context, t testing.TB, workdir, name, source string) {
	t.Helper()

	initGitRepo(ctx, t, workdir)

	_, err := hostDaggerExec(ctx, t, workdir, "workspace", "init")
	require.NoError(t, err)

	_, err = hostDaggerExec(ctx, t, workdir, "module", "init", "--sdk=dang", name)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(workdir, ".dagger", "modules", name, "main.dang"), []byte(source), 0o644))

	_, err = hostDaggerExec(ctx, t, workdir, "workspace", "config", "modules."+name+".entrypoint", "true")
	require.NoError(t, err)
}

// TestSingleQueryWorkspaceModuleLoadingSkipsUnreferencedBrokenModules locks in
// the user-visible behavior behind the SingleQuery optimization. A single raw
// GraphQL query that names one workspace module should only load that module;
// unrelated workspace modules must not be loaded eagerly just because they are
// present in the workspace config.
func (WorkspaceSuite) TestSingleQueryWorkspaceModuleLoadingSkipsUnreferencedBrokenModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile(".dagger/config.toml", `[modules.good]
source = "modules/good"

[modules.bad]
source = "modules/bad"
`).
		With(moduleLoadingDangModule(".dagger/modules/good", "good", "Good", "ping", "healthy module loaded")).
		WithNewFile(".dagger/modules/bad/dagger.json", `{"name":"bad","sdk":{"source":"dang"}}`).
		WithNewFile(".dagger/modules/bad/main.dang", `
type Bad {
  pub broken: String! {
    this is intentionally invalid dang source
  }
}
`)

	t.Run("query naming only the healthy module succeeds", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{ good { ping } }`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"good":{"ping":"healthy module loaded"}}`, out)
	})

	t.Run("full schema query still loads every workspace module", func(ctx context.Context, t *testctx.T) {
		fullSchema := base.WithExec([]string{"dagger", "query"}, dagger.ContainerWithExecOpts{
			Stdin:                         `{ __schema { queryType { name } } }`,
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})

		errOut, err := fullSchema.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, errOut, "bad")
	})
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
