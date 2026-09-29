package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestNativeParentHistoryProvenance(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "native-history-parent")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*GitRef]{}))
	url, err := gitutil.ParseURL("https://example.test/repo.git")
	require.NoError(t, err)
	makeRemote := func(user string) dagql.ObjectResult[*GitRef] {
		backend := &RemoteGitRepository{URL: url, AuthUsername: user}
		repo := env.attach(t, ctx, cache, srv, user+"-repo", &GitRepository{Backend: backend, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
		ref := &gitutil.Ref{SHA: strings.Repeat("a", 40)}
		return env.attach(t, ctx, cache, srv, user+"-ref", &GitRef{Repo: repo, Ref: ref, Backend: &RemoteGitRef{Ref: ref, repo: backend}}).(dagql.ObjectResult[*GitRef])
	}
	parent, other := makeRemote("alice"), makeRemote("bob")
	dir := env.directory(t, ctx, cache, srv, "local", "owned-objects")
	sha := strings.Repeat("b", 40)
	backend := &LocalGitRepository{Directory: dir, CheckoutBase: &GitCheckoutBase{Parent: parent, CommitSHA: sha}}
	repo := env.attach(t, ctx, cache, srv, "child-repo", &GitRepository{Backend: backend, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	ref := &gitutil.Ref{SHA: sha}
	child := &GitRef{Repo: repo, Ref: ref, Backend: &LocalGitRef{Ref: ref, repo: backend}}
	ok, err := nativeParentHistoryCandidate(ctx, child, parent.Self())
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = nativeParentHistoryCandidate(ctx, child, other.Self())
	require.NoError(t, err)
	require.False(t, ok, "equal URL/SHA must not join authentication recipes")
	for _, tc := range []struct {
		name   string
		mutate func(*GitRef)
	}{
		{"non-tip", func(c *GitRef) { c.Ref = &gitutil.Ref{SHA: strings.Repeat("c", 40)} }},
		{"changed backend", func(c *GitRef) {
			c.Backend = &LocalGitRef{Ref: ref, repo: &LocalGitRepository{Directory: dir, CheckoutBase: backend.CheckoutBase}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *child
			tc.mutate(&copy)
			ok, err := nativeParentHistoryCandidate(ctx, &copy, parent.Self())
			require.NoError(t, err)
			require.False(t, ok)
		})
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = nativeParentHistoryCandidate(ctx, child, parent.Self())
	require.ErrorIs(t, err, context.Canceled)
	require.IsType(t, &RemoteGitRef{}, parent.Self().Backend, "public ref must never be replaced")
}

func TestNativeParentHistoryHeaders(t *testing.T) {
	source := historyRepo(t, "sha1")
	gitMirrorTestRun(t, source, "commit", "--allow-empty", "-m", "base")
	parent := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	gitMirrorTestRun(t, source, "commit", "--allow-empty", "-m", "child")
	child := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	gitMirrorTestRun(t, source, "checkout", "--orphan", "unrelated")
	gitMirrorTestRun(t, source, "commit", "--allow-empty", "-m", "unrelated")
	unrelated := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	gitMirrorTestRun(t, source, "replace", child, unrelated)
	require.NoError(t, os.WriteFile(filepath.Join(source, ".git/info/grafts"), []byte(child+" "+unrelated+"\n"), 0644))
	git := gitutil.NewGitCLI(gitutil.WithDir(source))
	before := localTreeSnapshot(t, source)
	ok, err := validateNativeParentHistory(t.Context(), git, parent, child)
	require.NoError(t, err)
	require.True(t, ok, "raw headers, not grafts/replacement refs")
	ok, err = validateNativeParentHistory(t.Context(), git, unrelated, child)
	require.NoError(t, err)
	require.False(t, ok, "annotation cannot invent parentage")
	ok, err = validateNativeParentHistory(t.Context(), git, parent, unrelated)
	require.NoError(t, err)
	require.False(t, ok)
	_, err = validateNativeParentHistory(t.Context(), git, parent, strings.Repeat("f", 40))
	require.Error(t, err, "missing objects must not fall back")
	require.Equal(t, before, localTreeSnapshot(t, source))
	require.NoError(t, os.WriteFile(filepath.Join(source, ".git/shallow"), []byte(parent+"\n"), 0644))
	ok, err = validateNativeParentHistory(t.Context(), git, parent, child)
	require.NoError(t, err)
	require.False(t, ok, "unsupported shallow storage keeps legacy join")
}
