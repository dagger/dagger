package core

// This file contains shared workspace fixtures, host-side helpers, and
// container setup for workspace-focused tests. It should not own behavior
// coverage directly.

import (
	"context"
	"encoding/json"
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
// dagger.toml. A git root enables workspace/lockfile detection; a
// native config opts into native workspace behavior and suppresses legacy
// dagger.json compat inference, so tests should add it explicitly when needed.
func workspaceBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return gitRepoBase(t, c)
}

// nativeWorkspaceBase adds the native workspace state created by
// `dagger workspace init`: a dagger.toml inside the git root.
func nativeWorkspaceBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return workspaceBase(t, c).With(daggerExec("workspace", "init"))
}

func workspaceFixture(t testing.TB, c *dagger.Client, fixture string) *dagger.Container {
	t.Helper()
	return workspaceBase(t, c).With(withWorkspaceFixture(t, c, ".", "workspaces/"+fixture))
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

// TestSingleQueryWorkspaceModuleLoadingSkipsUnreferencedBrokenModules locks in
// the user-visible behavior behind the SingleQuery optimization. A single raw
// GraphQL query that names one workspace module should only load that module;
// unrelated workspace modules must not be loaded eagerly just because they are
// present in the workspace config.
func (WorkspaceSuite) TestSingleQueryWorkspaceModuleLoadingSkipsUnreferencedBrokenModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "single-query-broken")

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

// TestWorkspaceGit exercises the happy path for the Workspace.git API from a
// real Dagger query. It checks that the reported HEAD commit matches the local
// repository, that a clean repository reports an empty uncommitted changeset,
// and that dirty state is detected for the whole repository even when the
// current workspace is a nested directory.
func (WorkspaceSuite) TestWorkspaceGit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("tracked.txt", "v1").
		WithNewFile("toolchains/gitinfo/.keep", "").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		WithNewFile("tracked.txt", "v2").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "head"})

	headCommit, err := base.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	headCommit = strings.TrimSpace(headCommit)

	out, err := base.With(daggerQuery(`{
  currentWorkspace {
    git {
      head { commit }
      uncommitted { isEmpty }
    }
  }
}`)).Stdout(ctx)
	require.NoError(t, err)

	var got struct {
		CurrentWorkspace struct {
			Git struct {
				Head struct {
					Commit string `json:"commit"`
				} `json:"head"`
				Uncommitted struct {
					IsEmpty bool `json:"isEmpty"`
				} `json:"uncommitted"`
			} `json:"git"`
		} `json:"currentWorkspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Equal(t, headCommit, got.CurrentWorkspace.Git.Head.Commit)
	require.True(t, got.CurrentWorkspace.Git.Uncommitted.IsEmpty)

	out, err = base.
		WithNewFile("dirty.txt", "dirty").
		With(daggerQuery(`{currentWorkspace{git{uncommitted{isEmpty}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"currentWorkspace":{"git":{"uncommitted":{"isEmpty":false}}}}`, out)

	out, err = base.
		WithWorkdir("/work/toolchains/gitinfo").
		WithNewFile("/work/root-dirty.txt", "dirty").
		With(daggerQuery(`{currentWorkspace{cwd git{uncommitted{isEmpty}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"currentWorkspace":{"cwd":"toolchains/gitinfo","git":{"uncommitted":{"isEmpty":false}}}}`, out)
}

// TestWorkspaceGitWorktreeUnsupported documents the first-pass worktree
// boundary for Workspace.git. A git worktree has a .git file pointing at
// metadata outside the workspace boundary; until that metadata can be copied
// deliberately, Workspace.git should fail explicitly instead of following the
// pointer or expanding filesystem access outside the workspace.
func (WorkspaceSuite) TestWorkspaceGitWorktreeUnsupported(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("tracked.txt", "v1").
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		WithExec([]string{"git", "worktree", "add", "/linked", "HEAD"}).
		WithWorkdir("/linked")

	_, err := ctr.With(daggerQuery(`{currentWorkspace{git{head{commit}}}}`)).Stdout(ctx)
	require.Error(t, err)
	requireErrOut(t, err, "git worktrees are not supported by Workspace.git yet")
}

// TestEntrypointWithFieldHidden verifies that the synthetic `with` field
// installed on Query for entrypoint constructors with arguments is hidden
// from user-facing CLI listings (`dagger functions`, `dagger call --help`)
// while remaining callable and introspectable.
func (WorkspaceSuite) TestEntrypointWithFieldHidden(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "workspace-entrypoint")

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
