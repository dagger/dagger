package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/util/gitutil"
)

// gitFetchedTestSource is a repository whose commits each hold more objects
// than git unpacks from a fetch, so fetching one at depth 1 into an empty
// mirror leaves one pack, as for a real repository.
func gitFetchedTestSource(t *testing.T) (source, base, next string) {
	t.Helper()
	source = t.TempDir()
	gitMirrorTestRun(t, source, "init", "--quiet", "--initial-branch=main")
	commit := func(message string) string {
		gitMirrorTestRun(t, source, "add", ".")
		gitMirrorTestRun(t, source, "-c", "user.name=Dagger", "-c", "user.email=dagger@localhost", "commit", "--quiet", "-m", message)
		return gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	}
	for i := range 150 {
		dir := filepath.Join(source, fmt.Sprintf("d%d", i%10))
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), []byte(fmt.Sprintf("file %d\n", i)), 0o644))
	}
	base = commit("base")
	require.NoError(t, os.WriteFile(filepath.Join(source, "d0", "f0"), []byte("changed\n"), 0o644))
	next = commit("next")
	return source, base, next
}

func gitTestObjects(t *testing.T, dir string) string {
	t.Helper()
	return gitMirrorTestRun(t, dir, "cat-file", "--batch-all-objects", "--batch-check=%(objectname) %(objecttype)")
}

// A checkout of the commit an empty mirror just fetched copies the mirror's
// pack instead of fetching the same objects out of it again, and ends up the
// same repository a fetch from the mirror makes, owing the mirror nothing.
func TestGitCheckoutFromFetchedPack(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	source, base, _ := gitFetchedTestSource(t)
	repo, git, mirror := gitMirrorTestRepo(t, source)
	gitMirrorTestFetch(t, repo, git, base, 1)
	fetched, err := gitMirrorFetchedPack(mirror, base, 1)
	require.NoError(t, err)
	require.NotNil(t, fetched)

	checkout := func(fetched *gitMirrorFetch) (string, string, string) {
		dir := t.TempDir()
		checkoutGit := gitutil.NewGitCLI(gitutil.WithWorkTree(dir), gitutil.WithGitDir(filepath.Join(dir, ".git")))
		tmpref, method, err := copyGitCheckout(ctx, checkoutGit, git, filepath.Join(dir, ".git"), &gitutil.Ref{SHA: base}, 1, fetched)
		require.NoError(t, err)
		return dir, tmpref, method
	}
	copied, copiedRef, method := checkout(fetched)
	require.Equal(t, "pack", method)
	fetchedDir, fetchedRef, method := checkout(nil)
	require.Equal(t, "fetch", method)

	require.Equal(t, gitTestObjects(t, fetchedDir), gitTestObjects(t, copied))
	wantShallow, err := os.ReadFile(filepath.Join(fetchedDir, ".git", "shallow"))
	require.NoError(t, err)
	gotShallow, err := os.ReadFile(filepath.Join(copied, ".git", "shallow"))
	require.NoError(t, err)
	require.Equal(t, string(wantShallow), string(gotShallow))

	require.NoError(t, os.RemoveAll(mirror))
	remotes := []GitRemote{{Name: "origin", URL: "https://example.com/repo", Implicit: true}}
	ref := &gitutil.Ref{SHA: base}
	require.NoError(t, finishGitCheckout(ctx, gitutil.NewGitCLI(gitutil.WithWorkTree(copied), gitutil.WithGitDir(filepath.Join(copied, ".git"))), remotes, "https://example.com/repo", ref, false, copiedRef, gitCheckoutFresh))
	require.NoError(t, finishGitCheckout(ctx, gitutil.NewGitCLI(gitutil.WithWorkTree(fetchedDir), gitutil.WithGitDir(filepath.Join(fetchedDir, ".git"))), remotes, "https://example.com/repo", ref, false, fetchedRef, gitCheckoutFresh))
	gitMirrorTestRun(t, copied, "fsck", "--full")
	require.Equal(t, base, gitMirrorTestRun(t, copied, "rev-parse", "HEAD"))
	require.Equal(t, "1", gitMirrorTestRun(t, copied, "rev-list", "--count", "HEAD"))
	require.Equal(t, gitMirrorTestRun(t, fetchedDir, "for-each-ref", "--format=%(refname) %(objectname)"), gitMirrorTestRun(t, copied, "for-each-ref", "--format=%(refname) %(objectname)"))
	require.Empty(t, gitMirrorTestRun(t, copied, "status", "--porcelain"))
	contents, err := os.ReadFile(filepath.Join(copied, "d0", "f0"))
	require.NoError(t, err)
	require.Equal(t, "file 0\n", string(contents))
}

// Only one fetch into an empty mirror, kept as one pack, gives a checkout
// its pack: a mirror holding more, a fetch git unpacked, or a checkout of
// another commit or depth copies by fetching, as before.
func TestGitCheckoutFetchedPackOnlyForItsFetch(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("second fetch", func(t *testing.T) {
		t.Parallel()
		source, base, next := gitFetchedTestSource(t)
		repo, git, mirror := gitMirrorTestRepo(t, source)
		gitMirrorTestFetch(t, repo, git, base, 1)
		gitMirrorTestFetch(t, repo, git, next, 1)
		fetched, err := gitMirrorFetchedPack(mirror, next, 1)
		require.NoError(t, err)
		require.Nil(t, fetched)
	})
	t.Run("unpacked fetch", func(t *testing.T) {
		t.Parallel()
		source, base, _ := gitMirrorTestSource(t)
		repo, git, mirror := gitMirrorTestRepo(t, source)
		gitMirrorTestFetch(t, repo, git, base, 1)
		fetched, err := gitMirrorFetchedPack(mirror, base, 1)
		require.NoError(t, err)
		require.Nil(t, fetched)
	})
	for _, tc := range []struct {
		name  string
		other func(base, next string) (string, int)
	}{
		{name: "another commit", other: func(_, next string) (string, int) { return next, 1 }},
		{name: "another depth", other: func(base, _ string) (string, int) { return base, 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source, base, next := gitFetchedTestSource(t)
			repo, git, mirror := gitMirrorTestRepo(t, source)
			gitMirrorTestFetch(t, repo, git, base, 1)
			fetched, err := gitMirrorFetchedPack(mirror, base, 1)
			require.NoError(t, err)
			require.NotNil(t, fetched)
			sha, depth := tc.other(base, next)
			if sha != base {
				gitMirrorTestFetch(t, repo, git, sha, depth)
			}
			dir := t.TempDir()
			checkoutGit := gitutil.NewGitCLI(gitutil.WithWorkTree(dir), gitutil.WithGitDir(filepath.Join(dir, ".git")))
			_, method, err := copyGitCheckout(ctx, checkoutGit, git, filepath.Join(dir, ".git"), &gitutil.Ref{SHA: sha}, depth, fetched)
			require.NoError(t, err)
			require.Equal(t, "fetch", method)
			require.True(t, strings.Contains(gitTestObjects(t, dir), sha))
		})
	}
}

// A fetch the remote refused by commit and retried by its named ref brought
// that ref's tip, which can reach history the pinned commit's checkout would
// not have at its depth. Its pack is not the checkout's, so the checkout
// copies by fetching.
func TestGitCheckoutFetchedPackNotAfterNamedRetry(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	source := t.TempDir()
	gitMirrorTestRun(t, source, "init", "--quiet", "--initial-branch=main")
	commit := func(message string) string {
		gitMirrorTestRun(t, source, "add", ".")
		gitMirrorTestRun(t, source, "-c", "user.name=Dagger", "-c", "user.email=dagger@localhost", "commit", "--quiet", "-m", message)
		return gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	}
	for i := range 150 {
		require.NoError(t, os.WriteFile(filepath.Join(source, fmt.Sprintf("f%d", i)), []byte(fmt.Sprintf("file %d\n", i)), 0o644))
	}
	root := commit("root")
	require.NoError(t, os.WriteFile(filepath.Join(source, "f0"), []byte("pinned\n"), 0o644))
	pinned := commit("pinned")
	gitMirrorTestRun(t, source, "checkout", "--quiet", "-b", "side", root)
	require.NoError(t, os.WriteFile(filepath.Join(source, "f1"), []byte("side\n"), 0o644))
	commit("side")
	gitMirrorTestRun(t, source, "checkout", "--quiet", "main")
	// The branch moved past the pinned commit to a merge after it was
	// resolved, so the retry fetches the merge's history instead.
	gitMirrorTestRun(t, source, "-c", "user.name=Dagger", "-c", "user.email=dagger@localhost", "merge", "--quiet", "--no-edit", "side")

	repo, git, mirror := gitMirrorTestRepo(t, source)
	git = git.New(gitutil.WithExec(func(_ context.Context, cmd *exec.Cmd) error {
		for _, arg := range cmd.Args {
			if arg == pinned+":"+fetchedGitRef(pinned) {
				return gitutil.ErrSHAFetchUnsupported
			}
		}
		return cmd.Run()
	}))
	refs := []*RemoteGitRef{{Ref: &gitutil.Ref{Name: "refs/heads/main", SHA: pinned}}}
	retried, err := repo.fetchObjects(ctx, git, 2, false, refs)
	require.NoError(t, err)
	require.True(t, retried)

	// The mirror holds one pack, but not the pinned commit's checkout.
	pack, err := gitMirrorFetchedPack(mirror, pinned, 2)
	require.NoError(t, err)
	require.NotNil(t, pack)
	checkout := func(fetched *gitMirrorFetch) (string, string) {
		dir := t.TempDir()
		checkoutGit := gitutil.NewGitCLI(gitutil.WithWorkTree(dir), gitutil.WithGitDir(filepath.Join(dir, ".git")))
		_, method, err := copyGitCheckout(ctx, checkoutGit, git, filepath.Join(dir, ".git"), &gitutil.Ref{SHA: pinned}, 2, fetched)
		require.NoError(t, err)
		return dir, method
	}
	packed, _ := checkout(pack)
	fetchedDir, _ := checkout(nil)
	require.NotEqual(t, gitTestObjects(t, fetchedDir), gitTestObjects(t, packed))

	fetched, err := gitMirrorFetchedForCheckout(mirror, true, 2, false, refs, retried)
	require.NoError(t, err)
	require.Nil(t, fetched)
	_, method := checkout(fetched)
	require.Equal(t, "fetch", method)
}
