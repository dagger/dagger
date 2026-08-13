package core

// Coverage for the half of the sparse host base's contract that is not
// self-evident: it must never make a file look deleted.
//
// A host-backed workspace stores its edits as a delta root (the edits applied
// to an empty base) diffed against a SPARSE base — host.directory including
// only the paths the overlay considers touched. Removals are therefore
// *inferred*, from whatever the base holds and the delta root does not.
//
// An include pattern that matches a directory matches everything under it, so a
// touched directory whose contents the delta root only partly owns pulls the
// whole host subtree into the base, and every sibling the edit never mentioned
// reads as a deletion. It is silent, it is proportional to the directory, and
// export writes it to the user's checkout. These tests pin the two ways an edit
// can name a directory it does not own.

import (
	"context"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// overlaySiblingRepo is a committed checkout with a populated pkg/ tree: the
// content an edit that merely writes *into* pkg/ must not disturb.
func overlaySiblingRepo(ctx context.Context, t *testctx.T) string {
	t.Helper()
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	for path, contents := range map[string]string{
		"pkg/keep.txt":        "keep",
		"pkg/also-keep.txt":   "also keep",
		"pkg/deep/keep.txt":   "deep keep",
		"other/untouched.txt": "untouched",
	} {
		full := filepath.Join(workdir, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(contents), 0o644))
	}
	gitCommitAll(ctx, t, workdir, "initial")
	return workdir
}

// requireOverlayKeepsSiblings asserts the three places the phantom deletions
// would show up: the overlay changeset (what export writes), git's uncommitted
// view (what status, diff and commit see), and — the one that actually costs
// the user something — the checkout after a save.
func requireOverlayKeepsSiblings(
	ctx context.Context,
	t *testctx.T,
	workdir string,
	ws *dagger.Workspace,
) {
	t.Helper()

	removed, err := ws.Changes().RemovedPaths(ctx)
	require.NoError(t, err)
	require.Empty(t, removed, "overlay changeset invented deletions")

	removed, err = ws.Git().Uncommitted().RemovedPaths(ctx)
	require.NoError(t, err)
	require.Empty(t, removed, "git's uncommitted view invented deletions")

	require.NoError(t, ws.Export(ctx))
	for _, survivor := range []string{
		"pkg/keep.txt",
		"pkg/also-keep.txt",
		"pkg/deep/keep.txt",
		"other/untouched.txt",
	} {
		require.FileExists(t, filepath.Join(workdir, filepath.FromSlash(survivor)),
			"save deleted a file no edit touched")
	}
}

// TestHostWorkspaceOverlayChangesetAddKeepsSiblings is the reported failure: a
// changeset that adds one file under a new nested directory reports a synthetic
// added-directory entry for every parent it had to create — `pkg/`, `pkg/sub/`,
// then the file. Carrying those parents into the touched set makes the sparse
// base the entire real `pkg/`, which the delta root does not have, so every
// sibling reads as deleted.
//
// This is the path commit replay takes (withCommitsFrom -> Workspace.withChanges
// with a delta computed against a target tree the new file is absent from), so a
// worker adding a new package could delete the rest of the tree in whoever
// pulled its work.
func (WorkspaceAPISuite) TestHostWorkspaceOverlayChangesetAddKeepsSiblings(ctx context.Context, t *testctx.T) {
	workdir := overlaySiblingRepo(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(workdir))

	// Exactly the shape replay produces: the new file over a tree without it.
	added := c.Directory().WithNewFile("pkg/sub/new.txt", "new")
	changes := added.Changes(c.Directory())

	// The synthetic parents are in the changeset itself — that is fine and is
	// what the overlay must not be fooled by.
	addedPaths, err := changes.AddedPaths(ctx)
	require.NoError(t, err)
	require.Contains(t, addedPaths, "pkg/", "precondition: changeset reports the parent directory")

	ws := c.CurrentWorkspace().WithChanges(changes)

	addedPaths, err = ws.Changes().AddedPaths(ctx)
	require.NoError(t, err)
	require.Contains(t, addedPaths, "pkg/sub/new.txt")

	requireOverlayKeepsSiblings(ctx, t, workdir, ws)
}

// TestHostWorkspaceOverlayNewDirectoryKeepsSiblings is the same failure reached
// without a changeset at all: withNewDirectory MERGES its source into the target
// path, so naming that path as touched claims a directory the delta root holds
// only the source's share of.
func (WorkspaceAPISuite) TestHostWorkspaceOverlayNewDirectoryKeepsSiblings(ctx context.Context, t *testctx.T) {
	workdir := overlaySiblingRepo(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(workdir))

	source := c.Directory().WithNewFile("added.txt", "added")
	ws := c.CurrentWorkspace().WithNewDirectory("pkg", source)

	addedPaths, err := ws.Changes().AddedPaths(ctx)
	require.NoError(t, err)
	require.Contains(t, addedPaths, "pkg/added.txt")

	requireOverlayKeepsSiblings(ctx, t, workdir, ws)
}

// TestHostWorkspaceOverlayScopedCommitKeepsSiblings walks the incident all the
// way through: add under a new nested directory, commit only that directory,
// and the staged commit must not carry the rest of pkg/ away with it. A commit
// is the durable form of the mistake — it outlives the session and is what
// crosses to other workspaces.
func (WorkspaceAPISuite) TestHostWorkspaceOverlayScopedCommitKeepsSiblings(ctx context.Context, t *testctx.T) {
	workdir := overlaySiblingRepo(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(workdir))

	added := c.Directory().WithNewFile("pkg/sub/new.txt", "new")
	ws := c.CurrentWorkspace().
		WithChanges(added.Changes(c.Directory())).
		WithCommit("add pkg/sub", commitTestDate, dagger.WorkspaceWithCommitOpts{
			Paths: []string{"pkg/sub"},
		})

	staged, err := ws.Git().StagedCommits(ctx)
	require.NoError(t, err)
	require.Len(t, staged, 1)
	removed, err := staged[0].Changes().RemovedPaths(ctx)
	require.NoError(t, err)
	require.Empty(t, removed, "scoped commit swept up paths outside its scope")

	require.NoError(t, ws.Export(ctx))
	for _, survivor := range []string{"pkg/keep.txt", "pkg/deep/keep.txt"} {
		require.FileExists(t, filepath.Join(workdir, filepath.FromSlash(survivor)))
	}
	require.FileExists(t, filepath.Join(workdir, filepath.FromSlash("pkg/sub/new.txt")))
}

// TestHostWorkspaceOverlayRemovalsStillReported is the other side of the
// contract, and the reason the fix cannot simply be "never trust a touched
// directory": a directory the edit REALLY removes must still land in the base,
// or the removal has nothing to diff against and quietly does nothing.
func (WorkspaceAPISuite) TestHostWorkspaceOverlayRemovalsStillReported(ctx context.Context, t *testctx.T) {
	workdir := overlaySiblingRepo(ctx, t)
	c := connect(ctx, t, dagger.WithWorkdir(workdir))

	ws := c.CurrentWorkspace().WithoutDirectory("pkg")

	// removedPaths is collapsed to the topmost removal, so the directory is the
	// whole report — which is also why requireOverlayKeepsSiblings asserting it
	// empty catches a phantom mass deletion.
	removed, err := ws.Changes().RemovedPaths(ctx)
	require.NoError(t, err)
	require.Contains(t, removed, "pkg/")

	require.NoError(t, ws.Export(ctx))
	require.NoFileExists(t, filepath.Join(workdir, filepath.FromSlash("pkg/deep/keep.txt")))
	require.NoFileExists(t, filepath.Join(workdir, filepath.FromSlash("pkg/keep.txt")))
	require.FileExists(t, filepath.Join(workdir, filepath.FromSlash("other/untouched.txt")))
}

// TestHostWorkspaceOverlayRemovalKeepsSiblings is the mirror image, and the
// second bug the removal guard turned up: the sparse base cannot hold
// `a/b/removed` without also holding `a` and `a/b`, and the delta root — which
// expresses the removal by NOT holding it — does not hold those ancestors
// either. They read as removed too, removals collapse to their topmost entry,
// and deleting one module takes its neighbours with it.
func (WorkspaceAPISuite) TestHostWorkspaceOverlayRemovalKeepsSiblings(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	for path, contents := range map[string]string{
		"nested/dir/doomed/file.txt":  "doomed",
		"nested/dir/sibling/file.txt": "sibling",
		"nested/other.txt":            "other",
	} {
		full := filepath.Join(workdir, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(contents), 0o644))
	}
	gitCommitAll(ctx, t, workdir, "initial")

	c := connect(ctx, t, dagger.WithWorkdir(workdir))
	ws := c.CurrentWorkspace().WithoutDirectory("nested/dir/doomed")

	removed, err := ws.Changes().RemovedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"nested/dir/doomed/"}, removed,
		"the removal reached past what it was asked to delete")

	require.NoError(t, ws.Export(ctx))
	require.NoFileExists(t, filepath.Join(workdir, filepath.FromSlash("nested/dir/doomed/file.txt")))
	require.FileExists(t, filepath.Join(workdir, filepath.FromSlash("nested/dir/sibling/file.txt")))
	require.FileExists(t, filepath.Join(workdir, filepath.FromSlash("nested/other.txt")))
}

// gitCommitAll stages and commits everything in workdir.
func gitCommitAll(ctx context.Context, t *testctx.T, workdir, message string) {
	t.Helper()
	runGit(ctx, t, workdir, "add", ".")
	runGit(ctx, t, workdir, "commit", "-m", message)
}
