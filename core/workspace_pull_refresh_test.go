package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

// A pull starts from a retained checkout, whose index has no stat data. Without
// a refresh, `reset --hard` rewrites every file of the worktree and `add -A`
// re-hashes every file, writing each existing object again, which freshens
// (on a snapshot: copies up) the packs. After refreshIndex, both only look.
func TestWorkspacePullRefreshesRetainedIndex(t *testing.T) {
	ctx := context.Background()
	source, _, tip := cowTestSource(t)
	checkout := freshRetainedCheckout(t, source, &gitutil.Ref{SHA: tip})
	gitBundleTestRun(t, checkout, "gc", "--quiet")
	run := func(dir string, args ...string) {
		t.Helper()
		_, err := runWorkspacePullGit(ctx, dir, nil, args...)
		require.NoError(t, err)
	}

	stale := cowCopy(t, checkout)
	worktree := objectFiles(t, filepath.Join(stale, "nested"))
	run(stale, "reset", "--hard", "HEAD")
	require.NotEqual(t, worktree, objectFiles(t, filepath.Join(stale, "nested")), "the bug this guards against: a stat-less index rewrites every file")

	stale = cowCopy(t, checkout)
	packs := objectFiles(t, filepath.Join(stale, ".git", "objects", "pack"))
	run(stale, "add", "-A", "-f")
	require.NotEqual(t, packs, objectFiles(t, filepath.Join(stale, ".git", "objects", "pack")), "and re-hashing every file freshens the packs")

	fresh := cowCopy(t, checkout)
	worktree = objectFiles(t, filepath.Join(fresh, "nested"))
	packs = objectFiles(t, filepath.Join(fresh, ".git", "objects", "pack"))
	require.True(t, refreshWorkspacePullIndex(ctx, fresh))
	run(fresh, "add", "-A", "-f")
	run(fresh, "reset", "--hard", "HEAD")
	require.Equal(t, worktree, objectFiles(t, filepath.Join(fresh, "nested")))
	require.Equal(t, packs, objectFiles(t, filepath.Join(fresh, ".git", "objects", "pack")))
	status, err := runWorkspacePullGit(ctx, fresh, nil, "status", "--porcelain")
	require.NoError(t, err)
	require.Empty(t, status)
}
