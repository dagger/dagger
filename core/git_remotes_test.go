package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func TestDefaultGitRemote(t *testing.T) {
	for _, tc := range []struct {
		name           string
		remotes        []GitRemote
		upstream, want string
	}{
		{name: "none"},
		{name: "sole", remotes: []GitRemote{{Name: "upstream"}}, want: "upstream"},
		{name: "origin before upstream", remotes: []GitRemote{{Name: "fork"}, {Name: "origin"}}, upstream: "fork", want: "origin"},
		{name: "upstream", remotes: []GitRemote{{Name: "fork"}, {Name: "trunk"}}, upstream: "trunk", want: "trunk"},
		{name: "ambiguous", remotes: []GitRemote{{Name: "fork"}, {Name: "trunk"}}},
		{name: "local upstream", remotes: []GitRemote{{Name: "fork"}, {Name: "trunk"}}, upstream: "."},
		{name: "missing upstream", remotes: []GitRemote{{Name: "fork"}, {Name: "trunk"}}, upstream: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := SelectDefaultGitRemote(tc.remotes, tc.upstream)
			if tc.want == "" {
				require.Nil(t, remote)
			} else {
				require.NotNil(t, remote)
				require.Equal(t, tc.want, remote.Name)
			}
		})
	}
}

func TestGitConfiguredRemoteURLs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config [][2]string
		want   GitRemote
	}{
		{
			name:   "first fetch URL",
			config: [][2]string{{"remote.origin.url", "https://example.test/first"}, {"remote.origin.url", "https://example.test/second"}},
			want:   GitRemote{Name: "origin", URL: "https://example.test/first"},
		},
		{
			name:   "fetch and push rewrites",
			config: [][2]string{{"remote.origin.url", "alias:repo"}, {"url.https://example.test/fetch/.insteadOf", "alias:"}, {"url.ssh://git@example.test/push/.pushInsteadOf", "alias:"}},
			want:   GitRemote{Name: "origin", URL: "https://example.test/fetch/repo", PushURL: "ssh://git@example.test/push/repo"},
		},
		{
			name:   "first explicit push URL",
			config: [][2]string{{"remote.origin.url", "https://example.test/fetch"}, {"remote.origin.pushurl", "alias:first"}, {"remote.origin.pushurl", "alias:second"}, {"url.ssh://git@example.test/.insteadOf", "alias:"}},
			want:   GitRemote{Name: "origin", URL: "https://example.test/fetch", PushURL: "ssh://git@example.test/first"},
		},
		{
			name:   "push only",
			config: [][2]string{{"remote.origin.pushurl", "https://example.test/push"}},
			want:   GitRemote{Name: "origin", PushURL: "https://example.test/push"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := historyRepo(t, "sha1")
			for _, entry := range tc.config {
				gitMirrorTestRun(t, dir, "config", "--add", entry[0], entry[1])
			}
			remotes, err := readGitConfigRemotes(t.Context(), gitutil.NewGitCLI(gitutil.WithDir(dir)))
			require.NoError(t, err)
			require.Equal(t, []GitRemote{tc.want}, remotes)
		})
	}
}

func TestGitRemoteSelectionAfterCheckoutEdits(t *testing.T) {
	for _, tc := range []struct {
		name      string
		commands  [][]string
		preserved bool
		upstream  string
	}{
		{name: "unchanged", preserved: true, upstream: "trunk"},
		{name: "detached", commands: [][]string{{"checkout", "--detach"}}, preserved: true, upstream: "trunk"},
		{name: "new commit", commands: [][]string{{"commit", "--allow-empty", "-m", "next"}}, preserved: true, upstream: "trunk"},
		{name: "unrelated config", commands: [][]string{{"config", "user.name", "New Author"}}, preserved: true, upstream: "trunk"},
		{name: "rename", commands: [][]string{{"remote", "rename", "trunk", "renamed"}}},
		{name: "remove", commands: [][]string{{"remote", "remove", "trunk"}}},
		{name: "fetch URL", commands: [][]string{{"remote", "set-url", "trunk", "https://example.test/new"}}},
		{name: "push rewrite", commands: [][]string{{"config", "url.ssh://git@example.test/.pushInsteadOf", "https://example.test/"}}},
		{name: "tracking", commands: [][]string{{"config", "branch.main.remote", "fork"}, {"config", "branch.main.merge", "refs/heads/main"}}, upstream: "fork"},
		{name: "new branch", commands: [][]string{{"checkout", "-b", "other"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := historyRepo(t, "sha1")
			historyCommit(t, dir, "base", "base")
			gitMirrorTestRun(t, dir, "remote", "add", "trunk", "https://example.test/trunk")
			gitMirrorTestRun(t, dir, "remote", "add", "fork", "https://example.test/fork")
			git := gitutil.NewGitCLI(gitutil.WithDir(dir))
			remotes, err := readGitConfigRemotes(t.Context(), git)
			require.NoError(t, err)
			require.NoError(t, writeGitRemoteSelection(t.Context(), git, remotes, "trunk"))
			for _, command := range tc.commands {
				gitMirrorTestRun(t, dir, command...)
			}
			saved, selection, err := readGitRemoteSelection(t.Context(), git)
			require.NoError(t, err)
			if tc.preserved {
				require.Equal(t, remotes, saved)
				require.NotNil(t, selection)
				require.Equal(t, "trunk", *selection)
			} else {
				require.Nil(t, selection, "changed Git configuration must invalidate captured routing")
			}
			branch := gitMirrorTestRun(t, dir, "rev-parse", "--symbolic-full-name", "HEAD")
			_, upstream, err := readGitRemoteSelectionForRef(t.Context(), git, branch)
			require.NoError(t, err)
			require.Equal(t, tc.upstream, upstream)
		})
	}
}

func TestImplicitGitRemoteRouting(t *testing.T) {
	upstream := ""
	implicit := GitRemote{Name: "origin", URL: "https://example.test/repo", Implicit: true}
	repo := &GitRepository{Remotes: []GitRemote{implicit}, UpstreamRemote: &upstream}
	remotes, _, err := repo.ConfiguredRemotes(t.Context())
	require.NoError(t, err)
	require.Equal(t, "origin", SelectDefaultGitRemote(remotes, "").Name)
	require.Nil(t, repo.RemoteConfig("origin"), "describing the source must not fix its push destination")
	// Registering the same URL explicitly must still disable caller rewrites.
	explicit := GitRemote{Name: "origin", URL: implicit.URL}
	repo.Remotes = WithGitRemote(repo.Remotes, explicit)
	require.Equal(t, &explicit, repo.RemoteConfig("origin"))
}

type containsTestRepository struct {
	GitRepositoryBackend
	git *gitutil.GitCLI
}

func (repo *containsTestRepository) mount(ctx context.Context, _ int, _ bool, _ []GitRefBackend, fn func(*gitutil.GitCLI) error) error {
	return fn(repo.git)
}

func TestGitContainsCachedRefs(t *testing.T) {
	ctx := t.Context()
	dir := historyRepo(t, "sha1")
	base := historyCommit(t, dir, "base", "base")
	tip := historyCommit(t, dir, "tip", "tip")
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv := newCoreDagqlServerForTest(t, &Query{})
	srv.InstallObject(dagql.NewClass[*GitRepository](srv))
	repo := volumeTestCachedObjectResult(t, ctx, cache, srv, "contains", "repository", &GitRepository{
		Backend: &containsTestRepository{git: gitutil.NewGitCLI(gitutil.WithDir(dir))},
	})
	ref := func(sha string) *GitRef {
		return &GitRef{Repo: repo, Ref: &gitutil.Ref{SHA: sha}}
	}
	historyForbidFetch(t)
	for range 2 {
		got, err := ref(tip).Contains(ctx, ref(base))
		require.NoError(t, err)
		require.True(t, got)
		got, err = ref(base).Contains(ctx, ref(tip))
		require.NoError(t, err)
		require.False(t, got)
	}
}

func TestGitContainsHistory(t *testing.T) {
	ctx := t.Context()
	dir := historyRepo(t, "sha1")
	root := historyCommit(t, dir, "root", "root")
	base := historyCommit(t, dir, "base", "base")
	gitMirrorTestRun(t, dir, "branch", "side")
	tip := historyCommit(t, dir, "tip", "tip")
	gitMirrorTestRun(t, dir, "checkout", "side")
	side := historyCommit(t, dir, "side", "side")
	gitMirrorTestRun(t, dir, "merge", "--no-ff", "-m", "merge", "main")
	merged := gitMirrorTestRun(t, dir, "rev-parse", "HEAD")
	gitMirrorTestRun(t, dir, "checkout", "--orphan", "unrelated")
	gitMirrorTestRun(t, dir, "rm", "-rf", ".")
	unrelated := historyCommit(t, dir, "unrelated", "unrelated")
	historyForbidFetch(t)
	for _, tc := range []struct {
		name, commit, ancestor string
		want                   bool
	}{
		{"equal", tip, tip, true}, {"ancestor", tip, root, true},
		{"descendant", root, tip, false}, {"divergent", side, tip, false},
		{"merge first parent", merged, side, true}, {"merge second parent", merged, tip, true},
		{"unrelated", tip, unrelated, false}, {"older", tip, base, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gitContains(ctx, gitutil.NewGitCLI(gitutil.WithDir(dir)), tc.commit, tc.ancestor)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	_, err := gitContains(ctx, gitutil.NewGitCLI(gitutil.WithDir(dir)), tip, strings.Repeat("f", 40))
	require.Error(t, err, "an invalid commit must not look like a negative ancestry result")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = historyRef(dir, tip).Contains(cancelled, historyRef(dir, tip))
	require.ErrorIs(t, err, context.Canceled)
}

func TestGitContainsShallowHistory(t *testing.T) {
	dir := historyRepo(t, "sha1")
	root := historyCommit(t, dir, "root", "root")
	base := historyCommit(t, dir, "base", "base")
	tip := historyCommit(t, dir, "tip", "tip")
	// Retain the objects but introduce a shallow boundary, so Git's negative
	// answer would hide an ancestor unless the caller notices the boundary.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "shallow"), []byte(base+"\n"), 0600))
	git := gitutil.NewGitCLI(gitutil.WithDir(dir))
	got, err := gitContains(t.Context(), git, tip, base)
	require.NoError(t, err)
	require.True(t, got)
	_, err = gitContains(t.Context(), git, tip, root)
	require.ErrorIs(t, err, ErrGitHistoryIncomplete)
	// The missing ancestors below base cannot turn tip into an ancestor of
	// base. Native workspace history must answer this without hydration.
	historyForbidFetch(t)
	got, err = gitContains(t.Context(), git, base, tip)
	require.NoError(t, err)
	require.False(t, got)
	gitMirrorTestRun(t, dir, "checkout", "-b", "side", base)
	side := historyCommit(t, dir, "side", "side")
	got, err = gitContains(t.Context(), git, side, tip)
	require.NoError(t, err)
	require.False(t, got, "divergence above the boundary is also complete")
	// A second boundary that does not contain tip leaves the answer unknown.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "shallow"), []byte(base+"\n"+side+"\n"), 0600))
	_, err = gitContains(t.Context(), git, side, tip)
	require.ErrorIs(t, err, ErrGitHistoryIncomplete)
}

func TestGitContainsIgnoresUnreachableShallowBoundary(t *testing.T) {
	dir := historyRepo(t, "sha1")
	root := historyCommit(t, dir, "root", "root")
	tip := historyCommit(t, dir, "tip", "tip")
	gitMirrorTestRun(t, dir, "checkout", "--orphan", "unrelated")
	gitMirrorTestRun(t, dir, "rm", "-rf", ".")
	boundary := historyCommit(t, dir, "unrelated", "unrelated")
	historyCommit(t, dir, "unrelated-tip", "unrelated tip")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "shallow"), []byte(boundary+"\n"), 0600))
	// root's entire history is known. Another branch's missing history
	// cannot make the newer tip an ancestor of root.
	historyForbidFetch(t)
	got, err := gitContains(t.Context(), gitutil.NewGitCLI(gitutil.WithDir(dir)), root, tip)
	require.NoError(t, err)
	require.False(t, got)
}

func TestGitContainsShallowBoundaryWalks(t *testing.T) {
	for _, count := range []int{0, 32} {
		t.Run(fmt.Sprintf("unrelated_%d", count), func(t *testing.T) {
			dir := historyRepo(t, "sha1")
			root := historyCommit(t, dir, "root", "root")
			boundary := historyCommit(t, dir, "base", "base")
			tip := historyCommit(t, dir, "tip", "tip")
			var boundaries []string
			if count > 0 {
				gitMirrorTestRun(t, dir, "checkout", "--orphan", "unrelated")
				gitMirrorTestRun(t, dir, "rm", "-rf", ".")
				for i := range count {
					boundaries = append(boundaries, historyCommit(t, dir, "unrelated", fmt.Sprintf("unrelated %d", i)))
				}
			}
			boundaries = append(boundaries, boundary)
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "shallow"), []byte(strings.Join(boundaries, "\n")+"\n"), 0600))
			walks := 0
			git := gitutil.NewGitCLI(gitutil.WithDir(dir), gitutil.WithExec(func(_ context.Context, cmd *exec.Cmd) error {
				for _, arg := range cmd.Args {
					if arg == "merge-base" || arg == "rev-list" {
						walks++
					}
				}
				return cmd.Run()
			}))
			_, err := gitContains(t.Context(), git, tip, root)
			require.ErrorIs(t, err, ErrGitHistoryIncomplete)
			require.Equal(t, 2, walks, "unrelated boundaries must not add ancestry walks")
		})
	}
}
