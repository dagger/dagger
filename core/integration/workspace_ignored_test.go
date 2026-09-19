package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Git ignore rules only ever hide untracked files: an ignored file an agent
// wrote into its overlay is not an uncommitted change, so it must neither show
// up in Workspace.git.uncommitted nor make a commit of those changes fail.
// Modifications and deletions of tracked paths stay visible even when they
// match a rule.
func (WorkspaceSuite) TestWorkspaceGitUncommittedHonorsGitignore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	service, url := gitService(ctx, t, c, c.Directory().WithNewFile("base.txt", "base").WithNewFile("tracked.log", "tracked"))
	origin := snapshotWorkspace(ctx, t, c, c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service}).Branch("main").AsWorkspace())
	// Ignore rules committed after tracked.log, so it is tracked and ignored.
	base := origin.WithNewFile(".gitignore", ".env\n*.log\nbuild/\n").With(func(ws *dagger.Workspace) *dagger.Workspace {
		return ws.WithCommit(ws.Git().Uncommitted(), "ignore rules", workspaceCommitDate)
	})

	t.Run("ignored additions are not uncommitted changes", func(ctx context.Context, t *testctx.T) {
		ws := base.
			WithNewFile(".env", "SECRET=1").
			WithNewFile("src.txt", "src").
			WithNewFile("logs/app.log", "log").
			WithNewFile("build/out.o", "obj").
			WithNewFile("build/nested/deep.o", "obj")
		added, err := ws.Git().Uncommitted().AddedPaths(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"src.txt"}, added)
		removed, err := ws.Git().Uncommitted().RemovedPaths(ctx)
		require.NoError(t, err)
		require.Empty(t, removed)

		// The files are still in the workspace, just not pending.
		contents, err := ws.File(".env").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "SECRET=1", contents)

		committed := ws.WithCommit(ws.Git().Uncommitted(), "commit", workspaceCommitDate)
		tree := committed.Git().Head().Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
		entries, err := tree.Entries(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{".gitignore", "base.txt", "src.txt", "tracked.log"}, entries)
		empty, err := committed.Git().Uncommitted().IsEmpty(ctx)
		require.NoError(t, err)
		require.True(t, empty)
	})

	t.Run("tracked paths matching a rule stay visible", func(ctx context.Context, t *testctx.T) {
		modified, err := base.WithNewFile("tracked.log", "changed").Git().Uncommitted().ModifiedPaths(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"tracked.log"}, modified)
		removed, err := base.WithoutFile("tracked.log").Git().Uncommitted().RemovedPaths(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"tracked.log"}, removed)
	})

	t.Run("overlay ignore rules apply", func(ctx context.Context, t *testctx.T) {
		added, err := base.WithNewFile(".gitignore", "").WithNewFile(".env", "SECRET=1").Git().Uncommitted().AddedPaths(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{".env"}, added)
	})
}
