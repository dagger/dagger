package core

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// These tests define the Workspace.fork contract: a fork reads and edits like
// the workspace it came from, but changes() reports only the edits made
// through the fork — not everything already staged. This is the isolation the
// polyfill's fork provided to every SDK module (#13769); staging through
// plain Workspace.changes stays cumulative.

// TestWorkspaceForkChanges covers fork isolation on value workspaces.
func (WorkspaceSuite) TestWorkspaceForkChanges(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := syntheticWorkspaceSource(c).AsWorkspace()

	t.Run("changes on a fork contain only the fork's edits", func(ctx context.Context, t *testctx.T) {
		ws := base.WithNewFile("pre.txt", "pre")
		fork := ws.Fork().WithNewFile("post.txt", "post")

		added, err := fork.Changes().AddedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"post.txt"}, added)

		// the workspace it forked from still reports everything staged on it
		added, err = ws.Changes().AddedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"pre.txt"}, added)
	})

	t.Run("a pristine fork has no changes", func(ctx context.Context, t *testctx.T) {
		empty, err := base.Fork().Changes().IsEmpty(ctx)
		require.NoError(t, err)
		require.True(t, empty)
	})

	t.Run("fork reads see edits staged before the fork", func(ctx context.Context, t *testctx.T) {
		contents, err := base.WithNewFile("pre.txt", "pre").Fork().File("pre.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "pre", contents)
	})

	t.Run("restaging a pre-fork file reports a modification", func(ctx context.Context, t *testctx.T) {
		ws := base.WithNewFile("file.txt", "v1")
		fork := ws.Fork().WithNewFile("file.txt", "v2")

		modified, err := fork.Changes().ModifiedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"file.txt"}, modified)

		added, err := fork.Changes().AddedPaths(ctx)
		require.NoError(t, err)
		require.Empty(t, added)
	})

	t.Run("removing a base file through a fork reports the removal", func(ctx context.Context, t *testctx.T) {
		removed, err := base.Fork().WithoutFile("README.md").Changes().RemovedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"README.md"}, removed)
	})
}

// TestWorkspaceForkChangesAtCwd covers the fork's path contract: changes are
// measured from the workspace cwd — matching how a client applies a returned
// changeset at its own cwd — and a change that cannot be expressed
// cwd-relative fails loudly instead of landing in the wrong place.
func (WorkspaceSuite) TestWorkspaceForkChangesAtCwd(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := syntheticWorkspaceSource(c).AsWorkspace(dagger.DirectoryAsWorkspaceOpts{
		Cwd: "/app",
	})

	t.Run("fork changes are measured from the cwd", func(ctx context.Context, t *testctx.T) {
		changes := ws.Fork().WithNewFile("f.txt", "x").Changes()

		added, err := changes.AddedPaths(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"f.txt"}, added)

		contents, err := changes.Layer().File("f.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "x", contents)
	})

	t.Run("a change above the cwd is an error", func(ctx context.Context, t *testctx.T) {
		_, err := ws.Fork().WithNewFile("/outside.txt", "x").Changes().AddedPaths(ctx)
		require.ErrorContains(t, err, "outside the current directory")
	})
}

// TestWorkspaceForkChangesOnHost covers the same isolation against a
// host-backed workspace, where the fork diffs newly touched paths against
// host content fetched sparsely — the way modules actually receive
// workspaces from the CLI.
func (WorkspaceSuite) TestWorkspaceForkChangesOnHost(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceFixture(t, c, "fork").
		WithNewFile("host.txt", "original")

	t.Run("fork edits stay isolated from pre-fork staging", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("fork-added")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "post.txt")
		require.NotContains(t, out, "pre.txt")
	})

	t.Run("removing a host file through a fork reports the removal", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("fork-removed")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "host.txt")
	})

	t.Run("overwriting a host file through a fork reports a modification", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerReportCall("fork-modified")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "host.txt")
	})
}
