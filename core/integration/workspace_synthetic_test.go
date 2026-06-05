package core

import (
	"context"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestDirectoryBackedSyntheticWorkspaceUsesSourceContent asserts the core
// caller contract for Directory.asWorkspace: the supplied Directory is the
// workspace backend. Filesystem APIs resolve from cwd, absolute paths resolve
// from the source root, and filters run against the source content rather than
// requiring a host workspace.
func (WorkspaceSuite) TestDirectoryBackedSyntheticWorkspaceUsesSourceContent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := syntheticWorkspaceSource(c).AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/app/nested",
	})

	cwd, err := ws.Cwd(ctx)
	require.NoError(t, err)
	require.Equal(t, "/app/nested", cwd)

	leaf, err := ws.File("leaf.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "leaf", leaf)

	root, err := ws.File("/README.md").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "root readme", root)

	found, err := ws.FindUp(ctx, "workspace.marker")
	require.NoError(t, err)
	require.Equal(t, "/workspace.marker", found)

	filtered, err := ws.Directory("/app", dagger.WorkspaceDirectoryOpts{Gitignore: true}).Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, filtered, "main.txt")
	requireEntry(t, filtered, "nested")
	requireNoEntry(t, filtered, "debug.log")

	unfiltered, err := ws.Directory("/app").Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, unfiltered, "debug.log")
}

// TestGitBackedSyntheticWorkspacesUseGitContent asserts the GitRepository
// variant of the same backend contract. Local repositories should expose the
// current directory content, including uncommitted files. Remote repositories
// should expose the fetched git content. For both local and remote sources,
// Workspace.git should report the same git state as the source GitRepository;
// callers should not need to care how the backend represents that state.
func (WorkspaceSuite) TestGitBackedSyntheticWorkspacesUseGitContent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("local repository keeps current worktree content", func(ctx context.Context, t *testctx.T) {
		repo := localSyntheticGitRepository(t, c)
		ws := repo.AsWorkspace(dagger.GitRepositoryAsWorkspaceOpts{Cwd: "/app"})

		contents, err := ws.File("dirty.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "uncommitted worktree content", contents)

		assertWorkspaceGitignoreFiltering(ctx, t, ws)
		assertWorkspaceGitHead(ctx, t, ws, repo.Head())
		assertWorkspaceHasUncommittedChanges(ctx, t, ws)
		assertSyntheticWorkspaceListsAreEmpty(ctx, t, ws)
	})

	t.Run("remote repository keeps fetched git content", func(ctx context.Context, t *testctx.T) {
		gitDaemon, repoURL := gitService(ctx, t, c, syntheticWorkspaceSource(c))
		repo := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon})
		ws := repo.AsWorkspace(dagger.GitRepositoryAsWorkspaceOpts{Cwd: "/app"})

		contents, err := ws.File("main.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "app main", contents)

		assertWorkspaceGitignoreFiltering(ctx, t, ws)
		assertWorkspaceGitHead(ctx, t, ws, repo.Head())
		assertWorkspaceHasNoUncommittedChanges(ctx, t, ws)
		assertSyntheticWorkspaceListsAreEmpty(ctx, t, ws)
	})
}

// TestSyntheticWorkspaceManagementAPIsDoNotDependOnHostState asserts that
// source-backed workspaces do not accidentally route non-filesystem workspace
// APIs through a caller host session. Listing APIs return empty results because
// no workspace modules are loaded; mutating APIs reject because there is no
// local workspace to write.
func (WorkspaceSuite) TestSyntheticWorkspaceManagementAPIsDoNotDependOnHostState(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	cases := []struct {
		name string
		ws   *dagger.Workspace
	}{
		{
			name: "directory",
			ws:   syntheticWorkspaceSource(c).AsWorkspace(),
		},
		{
			name: "local git repository",
			ws:   localSyntheticGitWorkspace(t, c),
		},
	}

	gitDaemon, repoURL := gitService(ctx, t, c, syntheticWorkspaceSource(c))
	cases = append(cases, struct {
		name string
		ws   *dagger.Workspace
	}{
		name: "remote git repository",
		ws: c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
			AsWorkspace(),
	})

	for _, tc := range cases {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			assertSyntheticWorkspaceListsAreEmpty(ctx, t, tc.ws)

			_, err := tc.ws.Install(ctx, "github.com/dagger/dagger/modules/wolfi")
			require.Error(t, err)
		})
	}
}

// TestSyntheticWorkspaceFindUpRejectsInvalidNames asserts that Workspace.findUp
// searches for one path element while walking parents. Accepting slash or dot
// segments would turn a name lookup into path traversal and make rootfs-backed
// and host-backed workspaces disagree.
func (WorkspaceSuite) TestSyntheticWorkspaceFindUpRejectsInvalidNames(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := syntheticWorkspaceSource(c).AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/app/nested",
	})

	for _, name := range []string{"", ".", "..", "../workspace.marker", "nested/leaf.txt"} {
		t.Run(name, func(ctx context.Context, t *testctx.T) {
			_, err := ws.FindUp(ctx, name)
			require.Error(t, err)
		})
	}
}

func syntheticWorkspaceSource(c *dagger.Client) *dagger.Directory {
	return c.Directory().
		WithNewFile(".gitignore", "*.log\nbuild/\n").
		WithNewFile("README.md", "root readme").
		WithNewFile("workspace.marker", "root marker").
		WithNewFile("root.log", "ignored").
		WithNewFile("build/root.bin", "ignored").
		WithNewFile("app/main.txt", "app main").
		WithNewFile("app/debug.log", "ignored").
		WithNewFile("app/nested/leaf.txt", "leaf")
}

func localSyntheticGitWorkspace(t testing.TB, c *dagger.Client) *dagger.Workspace {
	t.Helper()
	return localSyntheticGitRepository(t, c).
		AsWorkspace(dagger.GitRepositoryAsWorkspaceOpts{Cwd: "/app"})
}

func localSyntheticGitRepository(t testing.TB, c *dagger.Client) *dagger.GitRepository {
	t.Helper()

	repo := c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithWorkdir("/repo").
		WithDirectory(".", syntheticWorkspaceSource(c)).
		WithExec([]string{"git", "init"}).
		WithExec([]string{"git", "add", "-A"}).
		WithExec([]string{"git", "commit", "-m", "initial"}).
		WithNewFile("app/dirty.txt", "uncommitted worktree content").
		Directory("/repo")

	return repo.AsGit()
}

func assertWorkspaceGitignoreFiltering(ctx context.Context, t *testctx.T, ws *dagger.Workspace) {
	t.Helper()

	filtered, err := ws.Directory(".", dagger.WorkspaceDirectoryOpts{Gitignore: true}).Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, filtered, "main.txt")
	requireNoEntry(t, filtered, "debug.log")

	unfiltered, err := ws.Directory(".").Entries(ctx)
	require.NoError(t, err)
	requireEntry(t, unfiltered, "debug.log")
}

func assertWorkspaceGitHead(ctx context.Context, t *testctx.T, ws *dagger.Workspace, expected *dagger.GitRef) {
	t.Helper()

	expectedCommit, err := expected.Commit(ctx)
	require.NoError(t, err)

	actualCommit, err := ws.Git().Head().Commit(ctx)
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(expectedCommit), strings.TrimSpace(actualCommit))
}

func assertWorkspaceHasUncommittedChanges(ctx context.Context, t *testctx.T, ws *dagger.Workspace) {
	t.Helper()

	empty, err := ws.Git().Uncommitted().IsEmpty(ctx)
	require.NoError(t, err)
	require.False(t, empty)
}

func assertWorkspaceHasNoUncommittedChanges(ctx context.Context, t *testctx.T, ws *dagger.Workspace) {
	t.Helper()

	empty, err := ws.Git().Uncommitted().IsEmpty(ctx)
	require.NoError(t, err)
	require.True(t, empty)
}

func assertSyntheticWorkspaceListsAreEmpty(ctx context.Context, t *testctx.T, ws *dagger.Workspace) {
	t.Helper()

	checks, err := ws.Checks().List(ctx)
	require.NoError(t, err)
	require.Empty(t, checks)

	generators, err := ws.Generators().List(ctx)
	require.NoError(t, err)
	require.Empty(t, generators)

	services, err := ws.Services().List(ctx)
	require.NoError(t, err)
	require.Empty(t, services)

	modules, err := ws.ModuleList(ctx)
	require.NoError(t, err)
	require.Empty(t, modules)

	envs, err := ws.EnvList(ctx)
	require.NoError(t, err)
	require.Empty(t, envs)
}

func requireEntry(t require.TestingT, entries []string, name string) {
	if hasWorkspaceEntry(entries, name) {
		return
	}
	require.Failf(t, "missing workspace entry", "expected %q in %v", name, entries)
}

func requireNoEntry(t require.TestingT, entries []string, name string) {
	if !hasWorkspaceEntry(entries, name) {
		return
	}
	require.Failf(t, "unexpected workspace entry", "did not expect %q in %v", name, entries)
}

func hasWorkspaceEntry(entries []string, name string) bool {
	for _, entry := range entries {
		if entry == name || entry == name+"/" {
			return true
		}
	}
	return false
}
