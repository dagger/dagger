package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// keepGitDir is the deprecated repository-wide option. Ordinary callers use
// tree(discardGitDir: true); this test preserves compatibility with repositories
// created by callers that still set keepGitDir to false.
func (WorkspaceSuite) TestWorkspaceLegacyKeepGitDirFalse(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	daemon, url := gitService(ctx, t, c, c.Directory().
		WithNewFile("src/a.txt", "old-a").WithNewFile("src/b.txt", "old-b"))
	serviceID, err := daemon.ID(ctx)
	require.NoError(t, err)
	var result struct {
		Git struct{ ID dagger.ID }
	}
	// Raw GraphQL is required only for this deprecated constructor argument:
	// the generated Go SDK omits optional false values, while its default is true.
	require.NoError(t, c.Do(ctx, &dagger.Request{
		Query: `query($url: String!, $service: ID!) {
   git(url: $url, experimentalServiceHost: $service, keepGitDir: false) { id }
  }`,
		Variables: map[string]any{"url": url, "service": serviceID},
	}, &dagger.Response{Data: &result}))
	repo := dagger.Ref[*dagger.GitRepository](c, result.Git.ID)
	baseSHA, err := repo.Head().CommitSHA(ctx)
	require.NoError(t, err)
	entries, err := repo.Head().Tree().Entries(ctx)
	require.NoError(t, err)
	require.NotContains(t, entries, ".git/")
	require.NotContains(t, entries, ".git")

	t.Run("workspace metadata remains available", func(ctx context.Context, t *testctx.T) {
		metadata := repo.Head().AsWorkspace().Git().Directory()
		entries, err := metadata.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, entries, "HEAD")
		require.Contains(t, entries, "index")
		require.Contains(t, entries, "objects/")
		head, err := metadata.AsGit().Head().CommitSHA(ctx)
		require.NoError(t, err)
		require.Equal(t, baseSHA, head)
	})

	t.Run("scoped commits preserve history and pending edits", func(ctx context.Context, t *testctx.T) {
		ws := repo.Branch("main").AsWorkspace(dagger.GitRefAsWorkspaceOpts{Cwd: "src"}).
			WithNewFile("a.txt", "new-a").WithNewFile("b.txt", "new-b")
		id, err := ws.WithCommit("selected edit", workspaceCommitDate, dagger.WorkspaceWithCommitOpts{Paths: []string{"a.txt"}}).ID(ctx)
		require.NoError(t, err)
		committed := dagger.Ref[*dagger.Workspace](c, id)
		tree := committed.Git().Head().Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
		for file, want := range map[string]string{"src/a.txt": "new-a", "src/b.txt": "old-b"} {
			got, err := tree.File(file).Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, want, got, file)
		}
		pending, err := committed.Git().Uncommitted().ModifiedPaths(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"src/b.txt"}, pending)
		working, err := committed.File("b.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "new-b", working)
		head := committed.Git().Head()
		parents, err := head.TargetCommit().ParentShas(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{baseSHA}, parents)
		// Metadata reconstruction must retain history with a local backend too.
		metadata := committed.Git().Directory().AsGit().Head()
		commits, err := metadata.Log(ctx)
		require.NoError(t, err)
		require.Len(t, commits, 2)
		reconstructed, err := metadata.CommitSHA(ctx)
		require.NoError(t, err)
		sha, err := head.CommitSHA(ctx)
		require.NoError(t, err)
		require.Equal(t, sha, reconstructed)
	})
}
