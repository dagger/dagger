package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

type boundedGitLogBackend struct {
	GitRefBackend
	mountFn func(context.Context, int, bool, func(*gitutil.GitCLI) error) error
}

func (b boundedGitLogBackend) mount(ctx context.Context, depth int, includeTags bool, fn func(*gitutil.GitCLI) error) error {
	return b.mountFn(ctx, depth, includeTags, fn)
}

func TestGitLogBoundedHistory(t *testing.T) {
	ctx := t.Context()
	source := t.TempDir()
	date := ""
	sourceGit := gitutil.NewGitCLI(gitutil.WithDir(source), gitutil.WithConfig(map[string]string{
		"user.name": "Test User", "user.email": "test@example.com",
	}), gitutil.WithExec(func(ctx context.Context, cmd *exec.Cmd) error {
		cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
		return cmd.Run()
	}))
	run := func(args ...string) string {
		t.Helper()
		out, err := sourceGit.Run(ctx, args...)
		require.NoError(t, err)
		return strings.TrimSpace(string(out))
	}
	run("init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(source, "old.txt"), []byte("old"), 0600))
	run("add", ".")
	tree := run("write-tree")
	commit := func(name string, timestamp int, parents ...string) string {
		date = fmt.Sprintf("%d +0000", 1700000000+timestamp)
		args := []string{"commit-tree", tree, "-m", name}
		for _, parent := range parents {
			args = append(args, "-p", parent)
		}
		return run(args...)
	}
	root := commit("root", 0)
	main := root
	for i := 1; i <= 10; i++ {
		main = commit(fmt.Sprint("main", i), i, main)
	}
	// Skewed dates: an ancestor of the second parent is newer than both its
	// child and the first parent. Compare Git's actual traversal, not a sort.
	side1 := commit("side1", 40, root)
	side2 := commit("side2", 20, side1)
	merge := commit("merge", 30, main, side2)
	run("update-ref", "refs/heads/main", merge)

	for _, tc := range []struct {
		name  string
		sha   string
		limit int
		paths []string
		warm  string
	}{
		{name: "cold linear", sha: main, limit: 3},
		{name: "single commit", sha: merge, limit: 1},
		{name: "merge boundary at limit", sha: merge, limit: 2},
		{name: "merge skewed ancestor", sha: merge, limit: 3},
		{name: "merge with skewed dates", sha: merge, limit: 5},
		{name: "exhausted history", sha: main, limit: 20},
		{name: "path filter requires full history", sha: main, limit: 3, paths: []string{"old.txt"}},
		{name: "uneven warm merge", sha: merge, limit: 5, warm: "uneven"},
		{name: "warm shallow history", sha: main, limit: 3, warm: "shallow"},
		{name: "warm shallow merge", sha: merge, limit: 5, warm: "shallow"},
		{name: "warm complete history", sha: merge, limit: 5, warm: "complete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, git, _ := gitMirrorTestRepo(t, source)
			switch tc.warm {
			case "uneven":
				// A long first-parent chain makes mount's depth estimate pass,
				// despite the second parent still being a shallow boundary.
				gitMirrorTestFetch(t, repo, git, merge, 2)
				gitMirrorTestFetch(t, repo, git, main, 10)
			case "shallow":
				gitMirrorTestFetch(t, repo, git, tc.sha, tc.limit)
			case "complete":
				gitMirrorTestFetch(t, repo, git, tc.sha, 0)
			}
			fetches := 0
			git = git.New(gitutil.WithExec(func(_ context.Context, cmd *exec.Cmd) error {
				if slices.Contains(cmd.Args, "fetch") {
					fetches++
				}
				return cmd.Run()
			}))
			var depths []int
			backend := boundedGitLogBackend{mountFn: func(ctx context.Context, depth int, tags bool, fn func(*gitutil.GitCLI) error) error {
				depths = append(depths, depth)
				require.False(t, tags)
				gitMirrorTestFetch(t, repo, git, tc.sha, depth)
				return fn(git)
			}}
			ref := &GitRef{Backend: backend, Ref: &gitutil.Ref{SHA: tc.sha}}
			commits, err := ref.Log(ctx, GitLogOptions{Limit: tc.limit, Paths: tc.paths})
			require.NoError(t, err)
			args := []string{"rev-list", "-n", strconv.Itoa(tc.limit), tc.sha}
			if len(tc.paths) > 0 {
				args = append(args, "--")
				args = append(args, tc.paths...)
			}
			want := strings.Fields(run(args...))
			var got []string
			for _, meta := range commits {
				got = append(got, meta.SHA)
				raw := run("cat-file", "commit", meta.SHA)
				expected, err := parseGitCommitMetadata(meta.SHA, raw)
				require.NoError(t, err)
				require.Equal(t, expected, meta, "shallow commits must retain their real parents")
			}
			require.Equal(t, want, got)
			switch {
			case len(tc.paths) > 0:
				require.Equal(t, []int{0}, depths)
			case tc.warm == "uneven":
				require.Equal(t, []int{tc.limit, 0}, depths)
			default:
				require.Equal(t, []int{tc.limit}, depths, "a bounded log must not request full history")
			}
			if tc.warm == "shallow" || tc.warm == "complete" {
				require.Zero(t, fetches, "a warm log must not fetch")
			} else {
				require.Equal(t, 1, fetches)
			}
			if tc.name == "cold linear" {
				out, err := git.Run(ctx, "rev-list", "--count", tc.sha)
				require.NoError(t, err)
				require.Equal(t, "3", strings.TrimSpace(string(out)))
				_, err = git.Run(ctx, "cat-file", "-e", root)
				require.Error(t, err, "unrequested ancestors must not be downloaded")
			}
		})
	}
}

func TestGitLogShallowBoundaryLinkedWorktree(t *testing.T) {
	ctx := t.Context()
	source, base, tip := gitMirrorTestSource(t)
	repo, git, _ := gitMirrorTestRepo(t, source)
	gitMirrorTestFetch(t, repo, git, tip, 2)
	worktree := filepath.Join(t.TempDir(), "linked worktree")
	_, err := git.Run(ctx, "worktree", "add", "--detach", worktree, tip)
	require.NoError(t, err)
	linkedGit := gitutil.NewGitCLI(gitutil.WithDir(worktree))

	// The boundary file lives in the common directory, not this worktree's
	// git directory. Missing it would silently omit the base's ancestors.
	gitDir, err := linkedGit.GitDir(ctx)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(gitDir, "shallow"))
	require.ErrorIs(t, err, os.ErrNotExist)
	for _, tc := range []struct {
		name  string
		shas  []string
		limit int
		want  bool
	}{
		{name: "exhausted shallow history", shas: []string{tip, base}, limit: 3, want: true},
		{name: "boundary at limit", shas: []string{tip, base}, limit: 2},
		{name: "boundary before limit", shas: []string{base, tip}, limit: 2, want: true},
		{name: "single boundary commit", shas: []string{base}, limit: 1},
		{name: "no commits", limit: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gitLogReachesShallowBoundary(ctx, linkedGit, tc.shas, tc.limit)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParseGitCommitMetadata(t *testing.T) {
	raw := `tree 5209ad308282b6d6c7d6e4888cd807e29079248b
parent 85896b2097b208dfb0ed2abc937612a7f3bd64b7
parent d8fd70f701d906d86ef0ad0504d3869b42114124
author Andrea Luzzardi <aluzzardi@gmail.com> 1667499276 -0700
committer GitHub <noreply@github.com> 1667499276 -0700

Merge pull request #3661 from aluzzardi/docs-go-sdk-remove-mkdir

docs: go: remove unnecessary MkdirAll from snippets
`

	meta, err := parseGitCommitMetadata("c80ac2c13df7d573a069938e01ca13f7a81f0345", raw)
	require.NoError(t, err)
	require.Equal(t, "c80ac2c13df7d573a069938e01ca13f7a81f0345", meta.SHA)
	require.Equal(t, "c80ac2c", meta.ShortSHA)
	require.Equal(t, "Andrea Luzzardi", meta.AuthorName)
	require.Equal(t, "aluzzardi@gmail.com", meta.AuthorEmail)
	require.Equal(t, "GitHub", meta.CommitterName)
	require.Equal(t, "noreply@github.com", meta.CommitterEmail)
	require.Equal(t, "2022-11-03T11:14:36-07:00", meta.AuthoredDate)
	require.Equal(t, "2022-11-03T11:14:36-07:00", meta.CommittedDate)
	require.Equal(t, []string{
		"85896b2097b208dfb0ed2abc937612a7f3bd64b7",
		"d8fd70f701d906d86ef0ad0504d3869b42114124",
	}, meta.ParentSHAs)
	require.Equal(t, "Merge pull request #3661 from aluzzardi/docs-go-sdk-remove-mkdir\n\ndocs: go: remove unnecessary MkdirAll from snippets", meta.Message)
}

func TestParseGitCommitMetadataLenientTimezone(t *testing.T) {
	// git accepts timezone offsets stricter parsers reject (+051800 is real,
	// common in history imported from CVS/SVN); one such commit must not fail
	// metadata parsing, it just renders in UTC
	raw := `tree 5209ad308282b6d6c7d6e4888cd807e29079248b
author Imported History <import@example.com> 1667499276 +051800
committer Imported History <import@example.com> 1667499276 junk

imported commit
`

	meta, err := parseGitCommitMetadata("c80ac2c13df7d573a069938e01ca13f7a81f0345", raw)
	require.NoError(t, err)
	require.Equal(t, "2022-11-03T18:14:36Z", meta.AuthoredDate)
	require.Equal(t, "2022-11-03T18:14:36Z", meta.CommittedDate)
}

func TestParseGitTimezoneOffset(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		offset int
		ok     bool
	}{
		{"+0000", 0, true},
		{"-0700", -7 * 60 * 60, true},
		{"+0530", 5*60*60 + 30*60, true},
		{"+2359", 23*60*60 + 59*60, true},
		{"-2359", -(23*60*60 + 59*60), true},
		// git reads the digits as hours*100+minutes, whatever the width
		{"+0575", 5*60*60 + 75*60, true},
		// valid for git, but unrepresentable in RFC3339
		{"+051800", 0, false},
		{"+2400", 0, false},
		{"0500", 0, false},
		{"+05x0", 0, false},
		{"+-500", 0, false},
		{"+", 0, false},
		{"", 0, false},
	} {
		offset, ok := parseGitTimezoneOffset(tc.raw)
		require.Equal(t, tc.ok, ok, "offset %q", tc.raw)
		require.Equal(t, tc.offset, offset, "offset %q", tc.raw)
	}
}
