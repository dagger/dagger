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
	"time"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// The oracle uses a complete worktree and git commit, not commit-tree. Exact
// SHA equality covers the tree, parent, dates, identities and message bytes.
func TestGitNativeCommitMatchesCheckout(t *testing.T) {
	for _, scenario := range []string{"ordinary", "named commit", "packed", "attributes", "changed attributes", "deleted attributes", "signoff", "ignored", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			source := t.TempDir()
			run := func(dir string, env []string, args ...string) string {
				t.Helper()
				out, err := runWorkspaceCommitGit(ctx, dir, env, args...)
				require.NoError(t, err)
				return strings.TrimSpace(out)
			}
			write := func(dir, name, data string, mode os.FileMode) {
				t.Helper()
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(data), mode))
			}
			run(source, nil, "init", "-b", "main")
			write(source, "unchanged", "must remain without being staged or checked out", 0600)
			write(source, "edit", "before", 0600)
			write(source, "remove", "remove", 0600)
			write(source, "file-to-dir", "file", 0600)
			write(source, "dir-to-file/child", "child", 0600)
			write(source, ".gitignore", "*.ignored\n", 0600)
			write(source, "nested/.gitignore", "ignored\n", 0600)
			write(source, ".gitattributes", "*.txt text eol=lf ident\n", 0600)
			write(source, "nested/.gitattributes", "*.txt text eol=crlf ident\n", 0600)
			run(source, nil, "add", ".")
			run(source, nil, "commit", "-m", "base")
			parent := run(source, nil, "rev-parse", "HEAD")
			run(source, nil, "tag", "unrelated-tag")
			run(source, nil, "branch", "unrelated-branch")
			if scenario == "packed" {
				run(source, nil, "gc", "--prune=now")
			}
			oracle, native := filepath.Join(t.TempDir(), "oracle"), filepath.Join(t.TempDir(), "native")
			run(source, nil, "clone", "--no-hardlinks", source, oracle)
			run(source, nil, "clone", "--no-hardlinks", source, native)
			packTimes := map[string]time.Time{}
			if scenario == "packed" {
				// The production donor is mounted read-only. This filesystem-only
				// unit cannot enforce that mount, so assert the writable output's
				// packs are never freshened (which would trigger COW copy-up).
				packs, err := filepath.Glob(filepath.Join(native, ".git/objects/pack/*"))
				require.NoError(t, err)
				require.NotEmpty(t, packs)
				for _, pack := range packs {
					old := time.Unix(1000000000, 0)
					require.NoError(t, os.Chtimes(pack, old, old))
					packTimes[pack] = old
				}
			}
			paths := &ChangesetPaths{}
			apply := func(work string) error {
				// A sparse staging area must never hydrate ordinary parent files.
				if work != oracle {
					_, err := os.Lstat(filepath.Join(work, "unchanged"))
					require.ErrorIs(t, err, os.ErrNotExist)
				}
				switch scenario {
				case "ordinary", "named commit", "packed", "signoff":
					paths.Modified = []string{"edit"}
					paths.Added = []string{"file-to-dir/child", "dir-to-file", "exec", "link", "a\n:[literal]"}
					paths.AllRemoved = []string{"remove", "file-to-dir", "dir-to-file/child"}
					for _, name := range []string{"remove", "file-to-dir", "dir-to-file"} {
						require.NoError(t, os.RemoveAll(filepath.Join(work, name)))
					}
					write(work, "edit", "after", 0600)
					write(work, "file-to-dir/child", "child", 0600)
					write(work, "dir-to-file", "file", 0600)
					write(work, "exec", "#!/bin/sh\n", 0700)
					write(work, "a\n:[literal]", "\x00binary\xff", 0600)
					if scenario == "packed" {
						paths.Added = append(paths.Added, "reuse-packed-blob")
						write(work, "reuse-packed-blob", "must remain without being staged or checked out", 0600)
					}
					require.NoError(t, os.Symlink("edit", filepath.Join(work, "link")))
				case "attributes", "changed attributes", "deleted attributes":
					paths.Added = []string{"new.txt", "nested/new.txt"}
					write(work, "new.txt", "$Id: abc $\r\nhello\r\n", 0600)
					write(work, "nested/new.txt", "$Id: def $\r\nhello\r\n", 0600)
					if scenario == "changed attributes" {
						paths.Modified = []string{"nested/.gitattributes"}
						write(work, "nested/.gitattributes", "*.txt -text -ident\n", 0600)
					}
					if scenario == "deleted attributes" {
						paths.AllRemoved = []string{".gitattributes", "nested/.gitattributes"}
						require.NoError(t, os.Remove(filepath.Join(work, ".gitattributes")))
						require.NoError(t, os.Remove(filepath.Join(work, "nested/.gitattributes")))
					}
				case "ignored":
					paths.Added = []string{"nested/ignored"}
					write(work, "nested/ignored", "ignored", 0600)
				}
				return nil
			}
			require.NoError(t, apply(oracle)) // also defines the changeset paths
			opts := GitCommitOpts{Message: "subject\n\nbody", Date: "2025-01-02T03:04:05Z", AuthorName: "Author", AuthorEmail: "author@example.com", CommitterName: "Committer", CommitterEmail: "committer@example.com", CommitterDate: "2025-01-03T03:04:05Z", Signoff: scenario == "signoff"}
			env := []string{"GIT_LITERAL_PATHSPECS=1", "GIT_AUTHOR_NAME=" + opts.AuthorName, "GIT_AUTHOR_EMAIL=" + opts.AuthorEmail, "GIT_COMMITTER_NAME=" + opts.CommitterName, "GIT_COMMITTER_EMAIL=" + opts.CommitterEmail, "GIT_AUTHOR_DATE=" + opts.Date, "GIT_COMMITTER_DATE=" + opts.CommitterDate}
			var oracleErr error
			if stage := commitStagePaths(paths); len(stage) != 0 {
				_, oracleErr = runWorkspaceCommitGit(ctx, oracle, env, append([]string{"add", "-A", "--"}, stage...)...)
			}
			parentRef := &gitutil.Ref{SHA: parent, Name: "refs/heads/main"}
			switch scenario {
			case "attributes":
				parentRef.Name = "" // commit-ID parents keep a detached HEAD
			case "named commit":
				parentRef.Name = parent // schema ref(SHA) preserves SHA as Name
			}
			originalRef := *parentRef
			err := withNativeCommitIndex(ctx, filepath.Join(native, ".git"), filepath.Join(source, ".git", "objects"), parentRef, paths, opts, apply)
			require.Equal(t, originalRef, *parentRef, "the shared parent ref must not be mutated")
			if scenario == "ignored" {
				require.Error(t, oracleErr)
				require.Error(t, err)
				return
			}
			if scenario == "empty" {
				require.ErrorIs(t, err, ErrNothingToCommit)
				opts.AllowEmpty = true
				err = withNativeCommitIndex(ctx, filepath.Join(native, ".git"), filepath.Join(source, ".git", "objects"), parentRef, paths, opts, apply)
			}
			require.NoError(t, oracleErr)
			require.NoError(t, err)
			args := []string{"commit", "--allow-empty", "--no-verify", "--no-gpg-sign", "--cleanup=verbatim", "-m", opts.Message}
			if opts.Signoff {
				args = append(args, "--trailer", "Signed-off-by: Author <author@example.com>")
			}
			run(oracle, env, args...)
			nativeGit := filepath.Join(native, ".git")
			require.Equal(t, run(oracle, nil, "rev-parse", "HEAD"), run(nativeGit, nil, "rev-parse", "HEAD"))
			require.Equal(t, parent, run(source, nil, "rev-parse", "HEAD"))
			require.Equal(t, parent, run(source, nil, "rev-parse", "refs/heads/main"))
			require.Equal(t, parent, run(nativeGit, nil, "rev-parse", "refs/tags/unrelated-tag"))
			require.Equal(t, parent, run(nativeGit, nil, "rev-parse", "refs/remotes/origin/unrelated-branch"))
			if strings.HasPrefix(parentRef.Name, "refs/heads/") {
				require.Equal(t, run(nativeGit, nil, "rev-parse", "HEAD"), run(nativeGit, nil, "rev-parse", "refs/heads/main"))
			} else {
				require.Equal(t, parent, run(nativeGit, nil, "rev-parse", "refs/heads/main"))
				head, err := os.ReadFile(filepath.Join(nativeGit, "HEAD"))
				require.NoError(t, err)
				require.True(t, IsFullGitSHA(strings.TrimSpace(string(head))))
			}
			for pack, mtime := range packTimes {
				info, err := os.Stat(pack)
				require.NoError(t, err)
				require.Equal(t, mtime, info.ModTime(), "Git must not freshen inherited packs: %s", pack)
			}
			require.Equal(t, source, run(nativeGit, nil, "config", "remote.origin.url"))
			run(nativeGit, nil, "fsck", "--full")
			_, err = os.Stat(filepath.Join(nativeGit, "objects/info/alternates"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestGitNativeCommitPublicationIsRooted(t *testing.T) {
	outside, repo := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "victim")
	require.NoError(t, os.WriteFile(victim, []byte("unchanged"), 0600))
	root, err := os.OpenRoot(repo)
	require.NoError(t, err)
	defer root.Close()
	// Final symlinks and hardlinks must be replaced, not followed/truncated.
	require.NoError(t, os.Symlink(victim, filepath.Join(repo, "HEAD")))
	require.NoError(t, os.Link(victim, filepath.Join(repo, "config")))
	require.NoError(t, nativeCommitWriteFile(root, "HEAD", []byte("new head")))
	require.NoError(t, nativeCommitWriteFile(root, "config", []byte("new config")))
	contents, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "unchanged", string(contents))
	// Neither external nor internal ancestor symlinks may redirect refs or
	// object fanouts. Only paths receiving new writes are inspected.
	require.NoError(t, os.Symlink(outside, filepath.Join(repo, "refs")))
	require.ErrorIs(t, nativeCommitWriteFile(root, "refs/heads/main", []byte("head")), errNativeCommitUnsupported)
	newObjects := t.TempDir()
	object := "aa/" + strings.Repeat("b", 38)
	require.NoError(t, os.Mkdir(filepath.Join(newObjects, "aa"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(newObjects, object), []byte("new object"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(repo, "aa")))
	require.ErrorIs(t, copyNativeCommitObjects(t.Context(), newObjects, repo), errNativeCommitUnsupported)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, copyNativeCommitObjects(ctx, newObjects, repo), context.Canceled)
}

func TestGitNativeCommitRejectsGitlinks(t *testing.T) {
	ctx := t.Context()
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := runWorkspaceCommitGit(ctx, repo, nil, args...)
		require.NoError(t, err)
		return strings.TrimSpace(out)
	}
	run("init", "-b", "main")
	run("commit", "--allow-empty", "-m", "root")
	parent := run("rev-parse", "HEAD")
	run("update-index", "--add", "--cacheinfo", "160000,"+parent+",module")
	run("commit", "-m", "gitlink")
	parent = run("rev-parse", "HEAD")
	gitDir := filepath.Join(repo, ".git")
	err := withNativeCommitIndex(ctx, gitDir, filepath.Join(gitDir, "objects"), &gitutil.Ref{SHA: parent}, &ChangesetPaths{Modified: []string{"module/file"}}, GitCommitOpts{Message: "edit", Date: "2025-01-02T03:04:05Z"}, func(string) error {
		t.Fatal("unsupported submodule change must not apply")
		return nil
	})
	require.ErrorIs(t, err, errNativeCommitUnsupported)
	require.Equal(t, parent, run("rev-parse", "HEAD"))
}

func TestGitNativeCommitObjectMetrics(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(t.Context())
	ctx, span := provider.Tracer("native-test").Start(t.Context(), "native")
	source, dest := t.TempDir(), t.TempDir()
	for _, root := range []string{source, dest} {
		require.NoError(t, os.Mkdir(filepath.Join(root, "aa"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "aa", strings.Repeat("b", 38)), []byte("existing"), 0444))
	}
	require.NoError(t, os.WriteFile(filepath.Join(source, "aa", strings.Repeat("c", 38)), []byte("new object"), 0444))
	require.NoError(t, copyNativeCommitObjects(ctx, source, dest))
	span.End()
	ended := recorder.Ended()
	require.Len(t, ended, 1)
	metrics := map[string]int64{}
	for _, attr := range ended[0].Attributes() {
		metrics[string(attr.Key)] = attr.Value.AsInt64()
	}
	require.Equal(t, int64(1), metrics["dagger.git.native.new_objects"], "existing objects must not be counted")
	require.Equal(t, int64(len("new object")), metrics["dagger.git.native.new_object_bytes"])
}

func TestGitNativeCommitRefStorageEligibility(t *testing.T) {
	for _, storage := range []string{"files", "reftable"} {
		for _, bare := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/bare=%t", storage, bare), func(t *testing.T) {
				root := t.TempDir()
				args := []string{"init", "-b", "main", "--ref-format=" + storage}
				if bare {
					args = append(args, "--bare")
				}
				_, err := runWorkspaceCommitGit(t.Context(), root, nil, args...)
				require.NoError(t, err)
				before := localTreeSnapshot(t, root)
				gitDir, err := nativeCommitGitDir(t.Context(), root)
				if storage == "files" {
					require.NoError(t, err)
					want := filepath.Join(root, ".git")
					if bare {
						want = root
					}
					require.Equal(t, want, gitDir)
				} else {
					require.ErrorIs(t, err, errNativeCommitUnsupported)
					var reason nativeCommitUnsupportedReason
					require.ErrorAs(t, err, &reason)
					require.Equal(t, "ref-storage", string(reason))
					require.True(t, nativeCommitFallback(err))
				}
				require.Equal(t, before, localTreeSnapshot(t, root), "eligibility must not mutate storage")
			})
		}
	}
}

func TestGitNativeCommitStorageEligibility(t *testing.T) {
	for unsupported, reason := range map[string]string{
		"shallow": "shallow-history", "objects/info/alternates": "object-alternates",
		"commondir": "linked-worktree", "objects/pack/partial.promisor": "partial-repository",
	} {
		t.Run(unsupported, func(t *testing.T) {
			root := t.TempDir()
			_, err := runWorkspaceCommitGit(t.Context(), root, nil, "init", "--bare")
			require.NoError(t, err)
			gitDir, err := nativeCommitGitDir(t.Context(), root)
			require.NoError(t, err)
			require.Equal(t, root, gitDir)
			require.NoError(t, os.WriteFile(filepath.Join(root, unsupported), []byte("unsupported\n"), 0600))
			_, err = nativeCommitGitDir(t.Context(), root)
			require.ErrorIs(t, err, errNativeCommitUnsupported)
			var unsupportedReason nativeCommitUnsupportedReason
			require.ErrorAs(t, err, &unsupportedReason)
			require.Equal(t, reason, string(unsupportedReason))
		})
	}
}

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
