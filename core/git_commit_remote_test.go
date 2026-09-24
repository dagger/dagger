package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestRemoteCommitBaseProvenance(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "remote-commit-provenance")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	url, err := gitutil.ParseURL("https://example.test/repo.git")
	require.NoError(t, err)
	makeRef := func(key, username, name string) dagql.ObjectResult[*GitRef] {
		remote := &RemoteGitRepository{URL: url, AuthUsername: username}
		repo := env.attach(t, ctx, cache, srv, key+"-repo", &GitRepository{Backend: remote, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
		ref := &gitutil.Ref{Name: name, SHA: strings.Repeat("a", 40)}
		return env.attach(t, ctx, cache, srv, key+"-ref", &GitRef{Repo: repo, Ref: ref, Backend: &RemoteGitRef{Ref: ref, repo: remote}}).(dagql.ObjectResult[*GitRef])
	}
	parent := makeRef("parent", "alice", "refs/heads/main")
	other := makeRef("other", "bob", "refs/heads/main")
	tag := makeRef("tag", "alice", "refs/tags/v1")
	before := env.directory(t, ctx, cache, srv, "before", "same-filesystem")
	before.Self().Lazy = &DirectoryGitTreeLazy{LazyState: NewLazyState(), Ref: parent, DiscardGitDir: true}
	changes := &Changeset{Before: before}
	ok, err := GitCommitChangesetNativeBase(ctx, parent, changes)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = GitCommitChangesetNativeBase(ctx, other, changes)
	require.NoError(t, err)
	require.False(t, ok, "same URL, SHA and files do not confer authorization")
	before.Self().Lazy = &DirectoryGitTreeLazy{LazyState: NewLazyState(), Ref: tag, DiscardGitDir: true}
	// No query/snapshot manager or mirror is available: unsupported ref kinds
	// and invalid options must finish without trying a remote promotion.
	_, supported, err := GitCommitChangesetNative(ctx, tag, changes, GitCommitOpts{})
	require.NoError(t, err)
	require.False(t, supported)
	before.Self().Lazy = &DirectoryGitTreeLazy{LazyState: NewLazyState(), Ref: parent, DiscardGitDir: true}
	_, supported, err = GitCommitChangesetNative(ctx, parent, changes, GitCommitOpts{Date: "invalid"})
	require.ErrorContains(t, err, "RFC3339")
	require.True(t, supported)
	before.Self().Lazy = nil
	ok, err = GitCommitChangesetNativeBase(ctx, parent, changes)
	require.NoError(t, err)
	require.False(t, ok, "arbitrary snapshot equality is not provenance")
}

func TestRemoteCommitBaseIsolation(t *testing.T) {
	ctx := t.Context()
	source := historyRepo(t, "sha1")
	oddSource := filepath.Join(t.TempDir(), "donor:\"odd\npath")
	require.NoError(t, os.Rename(source, oddSource))
	source = oddSource
	require.NoError(t, os.WriteFile(filepath.Join(source, "public"), []byte(strings.Repeat("shared compression text\n", 1000)+"public\n"), 0644))
	gitMirrorTestRun(t, source, "add", ".")
	gitMirrorTestRun(t, source, "commit", "-m", "public")
	public := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	gitMirrorTestRun(t, source, "checkout", "--orphan", "private")
	require.NoError(t, os.WriteFile(filepath.Join(source, "public"), []byte(strings.Repeat("shared compression text\n", 1000)+"SECRET\n"), 0644))
	gitMirrorTestRun(t, source, "add", ".")
	gitMirrorTestRun(t, source, "commit", "-m", "private")
	private := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	gitMirrorTestRun(t, source, "repack", "-adf")
	// Replacement refs must neither alter the authorized closure nor leak.
	gitMirrorTestRun(t, source, "replace", public, private)
	before := localTreeSnapshot(t, source)
	want, err := runWorkspaceCommitGit(ctx, source, []string{"GIT_NO_REPLACE_OBJECTS=1"}, "rev-list", "--objects", "--no-object-names", public)
	require.NoError(t, err)

	// Overlapping promotions share a read-only donor, never destination refs,
	// indexes or alternate files. The donor may pack both auth scopes together.
	destinations := []string{t.TempDir(), t.TempDir()}
	errs := make([]error, len(destinations))
	var wg sync.WaitGroup
	for i, dest := range destinations {
		wg.Go(func() {
			errs[i] = packRemoteCommitBase(ctx, source, dest, public, []GitRemote{{Name: "origin", URL: "https://fetch.test/repo", PushURL: "ssh://push.test/repo"}})
		})
	}
	wg.Wait()
	for i, dest := range destinations {
		require.NoError(t, errs[i])
		got := gitMirrorTestRun(t, dest, "cat-file", "--batch-all-objects", "--batch-check=%(objectname)")
		require.ElementsMatch(t, strings.Fields(want), strings.Fields(got), "no unauthorized loose objects or pack delta bases")
		require.Empty(t, gitMirrorTestRun(t, dest, "for-each-ref"))
		require.Equal(t, public, gitMirrorTestRun(t, dest, "rev-parse", "HEAD"))
		require.Equal(t, "ssh://push.test/repo", gitMirrorTestRun(t, dest, "remote", "get-url", "--push", "origin"))
		_, err := os.Stat(filepath.Join(dest, "objects/info/alternates"))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	require.Equal(t, before, localTreeSnapshot(t, source), "promotion must not change donor refs/config/objects")
	require.NoError(t, os.RemoveAll(source))
	for _, dest := range destinations {
		gitMirrorTestRun(t, dest, "fsck", "--full", "--strict")
		require.Equal(t, "public", gitMirrorTestRun(t, dest, "log", "-1", "--format=%s"))
	}
}

func TestRemoteCommitBaseFallbackAndErrors(t *testing.T) {
	for _, kind := range []string{"shallow", "alternates", "partial", "missing", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			source := historyRepo(t, "sha1")
			gitMirrorTestRun(t, source, "commit", "--allow-empty", "-m", "base")
			sha := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
			ctx := t.Context()
			switch kind {
			case "shallow":
				require.NoError(t, os.WriteFile(filepath.Join(source, ".git/shallow"), []byte(sha+"\n"), 0644))
			case "alternates":
				require.NoError(t, os.WriteFile(filepath.Join(source, ".git/objects/info/alternates"), []byte("/expired/mount\n"), 0644))
			case "partial":
				gitMirrorTestRun(t, source, "config", "extensions.partialClone", "origin")
			case "missing":
				sha = strings.Repeat("a", 40)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := packRemoteCommitBase(ctx, source, t.TempDir(), sha, nil)
			require.Error(t, err)
			require.Equal(t, kind != "missing" && kind != "canceled", nativeCommitFallback(err))
		})
	}
}
