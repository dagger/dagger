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

func dirHistorySource(dir string, donor bool, anchor string) *donorHistorySource {
	src := &donorHistorySource{donor: donor, anchor: anchor, gitDir: donorGitDir}
	src.mount = func(_ context.Context, fn func(*gitutil.GitCLI) error) error {
		return fn(gitutil.NewGitCLI(gitutil.WithDir(dir)))
	}
	if !donor {
		src.gitDir = func(ctx context.Context, root string) (string, error) {
			return nativeCommitGitDirWithShallow(ctx, root, true)
		}
	}
	return src
}

func TestDonorHistoryJoin(t *testing.T) {
	ctx := t.Context()
	host := historyRepo(t, "sha1")
	historyCommit(t, host, "a", "one")
	upstream := historyCommit(t, host, "b", "two")
	hostHead := historyCommit(t, host, "c", "three")

	// An owned depth-one checkout of upstream with an agent commit on top.
	owned := t.TempDir()
	require.NoError(t, packRemoteCommitBaseDepth(ctx, host, owned, upstream, nil, 1))
	opts := GitCommitOpts{Message: "agent", Date: "2026-01-01T00:00:00Z", AuthorName: "Agent", AuthorEmail: "agent@example.com"}
	require.NoError(t, withNativeCommitIndex(ctx, owned, filepath.Join(owned, "objects"), &gitutil.Ref{SHA: upstream}, &ChangesetPaths{Added: []string{"agent"}}, opts, func(work string) error {
		return os.WriteFile(filepath.Join(work, "agent"), []byte("agent\n"), 0644)
	}))
	child := gitMirrorTestRun(t, owned, "rev-parse", "HEAD")
	shallowClone := t.TempDir()
	gitMirrorTestRun(t, shallowClone, "clone", "--depth=1", "file://"+host, ".")
	unrelated := historyRepo(t, "sha1")
	historyCommit(t, unrelated, "x", "unrelated")
	// A complete copy of host whose head is grafted parentless. Git would let
	// gc prune the hidden ancestors, which the donor view would still walk.
	grafted := t.TempDir()
	gitMirrorTestRun(t, grafted, "clone", "--no-local", "file://"+host, ".")
	graftsPath := gitMirrorTestRun(t, grafted, "rev-parse", "--path-format=absolute", "--git-path", "info/grafts")
	require.NoError(t, os.MkdirAll(filepath.Dir(graftsPath), 0o755))
	require.NoError(t, os.WriteFile(graftsPath, []byte(hostHead+"\n"), 0o644))

	hostBefore := historySnapshot(t, host)
	ownedBefore := historySnapshot(t, owned)
	historyForbidFetch(t)

	revList := func(git *gitutil.GitCLI, args ...string) []string {
		out, err := git.Run(ctx, append([]string{"rev-list"}, args...)...)
		require.NoError(t, err)
		return strings.Fields(string(out))
	}

	t.Run("remote ref covered by host", func(t *testing.T) {
		called := false
		handled, err := joinDonorHistory(ctx, []*donorHistorySource{dirHistorySource(host, true, "")}, []string{upstream}, []string{upstream, hostHead}, func(git *gitutil.GitCLI, shas []string) error {
			called = true
			require.Equal(t, []string{upstream, hostHead}, shas)
			require.Empty(t, revList(git, shas[0], "^"+shas[1]))
			require.Equal(t, []string{hostHead}, revList(git, shas[1], "^"+shas[0]))
			return nil
		})
		require.NoError(t, err)
		require.True(t, handled)
		require.True(t, called)
	})

	t.Run("owned shallow boundary covered by host", func(t *testing.T) {
		sources := []*donorHistorySource{dirHistorySource(owned, false, upstream), dirHistorySource(host, true, "")}
		handled, err := joinDonorHistory(ctx, sources, nil, []string{child, hostHead}, func(git *gitutil.GitCLI, shas []string) error {
			require.Equal(t, []string{child}, revList(git, shas[0], "^"+shas[1]))
			require.Equal(t, []string{hostHead}, revList(git, shas[1], "^"+shas[0]))
			// The boundary is dropped: history continues through the donor.
			require.Len(t, revList(git, shas[0]), 3)
			base, err := git.Run(ctx, "merge-base", shas[0], shas[1])
			require.NoError(t, err)
			require.Equal(t, upstream, strings.TrimSpace(string(base)))
			return nil
		})
		require.NoError(t, err)
		require.True(t, handled)
	})

	for _, tc := range []struct {
		name    string
		sources []*donorHistorySource
		needed  []string
		shas    []string
	}{
		{"donor lacks the commit", []*donorHistorySource{dirHistorySource(unrelated, true, "")}, []string{upstream}, []string{upstream, upstream}},
		{"donor lacks the boundary", []*donorHistorySource{dirHistorySource(owned, false, upstream), dirHistorySource(unrelated, true, "")}, nil, []string{child, child}},
		{"shallow donor", []*donorHistorySource{dirHistorySource(shallowClone, true, "")}, []string{hostHead}, []string{hostHead, hostHead}},
		{"grafted donor", []*donorHistorySource{dirHistorySource(grafted, true, "")}, []string{hostHead}, []string{hostHead, hostHead}},
		{"no donor", []*donorHistorySource{dirHistorySource(owned, false, upstream)}, []string{upstream}, []string{child, upstream}},
		{"nothing to cover", []*donorHistorySource{dirHistorySource(host, true, "")}, nil, []string{hostHead, upstream}},
		{"unexpected boundary", []*donorHistorySource{dirHistorySource(owned, false, hostHead), dirHistorySource(host, true, "")}, nil, []string{child, hostHead}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handled, err := joinDonorHistory(ctx, tc.sources, tc.needed, tc.shas, func(*gitutil.GitCLI, []string) error {
				t.Fatal("uncovered history must be left to the existing join")
				return nil
			})
			require.NoError(t, err)
			require.False(t, handled)
		})
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := joinDonorHistory(canceled, []*donorHistorySource{dirHistorySource(host, true, "")}, []string{upstream}, []string{upstream}, func(*gitutil.GitCLI, []string) error { return nil })
	require.ErrorIs(t, err, context.Canceled)

	require.Equal(t, hostBefore, historySnapshot(t, host), "donor storage is read-only")
	require.Equal(t, ownedBefore, historySnapshot(t, owned), "owned storage keeps its boundary")
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
