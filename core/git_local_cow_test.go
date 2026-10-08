package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

// cowTestSource builds a repository exercising everything a retained checkout
// carries: packed and loose objects, reachable and unreachable lightweight and
// annotated tags, remotes with an upstream, modes, symlinks and a dirty
// worktree that must not leak into the result.
func cowTestSource(t *testing.T) (dir, base, tip string) {
	t.Helper()
	dir = historyRepo(t, "sha1")
	base = historyCommit(t, dir, "file", "base")
	gitMirrorTestRun(t, dir, "tag", "v0.1", base)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested", "deeper"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nested", "deeper", "file"), []byte("nested"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "executable"), []byte("#!/bin/sh\n"), 0755))
	require.NoError(t, os.Symlink("file", filepath.Join(dir, "link")))
	// Enough files for Git's parallel checkout to engage.
	for i := range 150 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "nested", fmt.Sprintf("many%d", i)), []byte(fmt.Sprint(i)), 0644))
	}
	gitMirrorTestRun(t, dir, "add", ".")
	gitMirrorTestRun(t, dir, "commit", "-m", "modes")
	gitMirrorTestRun(t, dir, "tag", "-a", "-m", "annotated", "v0.2")
	gitMirrorTestRun(t, dir, "gc", "--quiet")
	// Unreachable from the tip: a fresh checkout's fetch never follows these.
	gitMirrorTestRun(t, dir, "checkout", "--quiet", "-b", "side", base)
	side := historyCommit(t, dir, "side", "side")
	gitMirrorTestRun(t, dir, "tag", "side-light", side)
	gitMirrorTestRun(t, dir, "tag", "-a", "-m", "side annotated", "side-annotated", side)
	gitMirrorTestRun(t, dir, "checkout", "--quiet", "main")
	// Loose objects on top of the pack.
	tip = historyCommit(t, dir, "file", "tip")
	gitMirrorTestRun(t, dir, "tag", "-a", "-m", "tip annotated", "v0.3")
	gitMirrorTestRun(t, dir, "remote", "add", "origin", "https://example.com/repo.git")
	gitMirrorTestRun(t, dir, "remote", "add", "fork", "https://example.com/fork.git")
	gitMirrorTestRun(t, dir, "config", "remote.fork.pushurl", "https://example.com/push.git")
	gitMirrorTestRun(t, dir, "config", "branch.main.remote", "fork")
	gitMirrorTestRun(t, dir, "config", "branch.main.merge", "refs/heads/main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), []byte("dirty"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked"), []byte("untracked"), 0644))
	return dir, base, tip
}

// freshRetainedCheckout is LocalGitRef.Tree's full path: fetch everything
// into an empty directory.
func freshRetainedCheckout(t *testing.T, src string, ref *gitutil.Ref) string {
	t.Helper()
	ctx := t.Context()
	dest := t.TempDir()
	source := gitutil.NewGitCLI(gitutil.WithDir(src))
	url, err := source.URL(ctx)
	require.NoError(t, err)
	remotes, upstream, err := localCheckoutRemotes(ctx, source, ref.Name, nil, nil)
	require.NoError(t, err)
	checkoutGit := localTreeCheckoutCLI(dest)
	require.NoError(t, doGitCheckout(ctx, checkoutGit, remotes, url, ref, 0, false))
	require.NoError(t, writeGitRemoteSelection(ctx, checkoutGit, remotes, upstream))
	return dest
}

// cowCopy stands in for the copy-on-write child snapshot of a source tree.
func cowCopy(t testing.TB, src string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "root")
	out, err := exec.Command("cp", "-a", src, dest).CombinedOutput()
	require.NoError(t, err, string(out))
	return dest
}

// cowWipeCheckout is the wipe path: root is a copy of the repository at src.
func cowWipeCheckout(ctx context.Context, root, src string, ref *gitutil.Ref) error {
	return cowGitCheckout(ctx, root, gitutil.NewGitCLI(gitutil.WithDir(src)), nil, nil, ref, nil)
}

// cowDeltaCheckout is the delta path: root is a copy of base's full checkout,
// source the repository holding ref.
func cowDeltaCheckout(ctx context.Context, root, source, base string, ref *gitutil.Ref) error {
	git := gitutil.NewGitCLI(gitutil.WithDir(source))
	plan, reason, err := planIncrementalGitCheckout(ctx, git, base, ref.SHA, false)
	if err != nil {
		return err
	}
	if reason != "" {
		return nativeCommitUnsupportedReason(reason)
	}
	return cowGitCheckout(ctx, root, git, nil, nil, ref, &cowCheckoutDelta{base: base, plan: plan})
}

// objectFiles identifies each inherited object file by inode, size and
// mtime: a copy-up or rewrite (e.g. Git freshening a pack) changes them.
func objectFiles(t *testing.T, objects string) map[string]string {
	t.Helper()
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(objects, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files[path] = fmt.Sprintf("%d %d %d", info.Sys().(*syscall.Stat_t).Ino, info.Size(), info.ModTime().UnixNano())
		return nil
	}))
	return files
}

// withoutObjects drops the object store, whose contents legitimately differ,
// and the Git directory's own mtime, which no checkout normalizes: the final
// configuration write replaces a file in it.
func withoutObjects(snapshot map[string]string) map[string]string {
	maps.DeleteFunc(snapshot, func(path, _ string) bool {
		return path == ".git" || path == filepath.Join(".git", "objects") || strings.HasPrefix(path, filepath.Join(".git", "objects")+string(filepath.Separator))
	})
	return snapshot
}

func TestCowGitCheckout(t *testing.T) {
	ctx := context.Background()
	source, base, tip := cowTestSource(t)
	// A native commit result: a bare Git directory selected below a stale
	// worktree in the same snapshot.
	bareSelected := cowCopy(t, source)
	gitMirrorTestRun(t, bareSelected, "config", "core.bare", "true")
	require.NoError(t, os.WriteFile(filepath.Join(bareSelected, "stale"), []byte("stale"), 0644))

	for _, layout := range []struct {
		name, root, src string
	}{
		{"checkout", source, ""},
		{"bare git directory", bareSelected, ".git"},
	} {
		for _, ref := range []*gitutil.Ref{
			{Name: "refs/heads/main", SHA: tip},
			{SHA: base},
			// Commit-ID resolvers keep the SHA as the name.
			{Name: tip, SHA: tip},
			{Name: "refs/tags/v0.2", SHA: gitMirrorTestRun(t, source, "rev-parse", "v0.2^{commit}")},
			{Name: "refs/heads/side", SHA: gitMirrorTestRun(t, source, "rev-parse", "side")},
		} {
			t.Run(fmt.Sprintf("%s/%s@%s", layout.name, ref.Name, ref.SHA[:7]), func(t *testing.T) {
				baseline := freshRetainedCheckout(t, filepath.Join(layout.root, layout.src), ref)
				root := cowCopy(t, layout.root)
				inherited := objectFiles(t, filepath.Join(root, ".git", "objects"))
				historyForbidFetch(t)
				require.NoError(t, cowWipeCheckout(ctx, root, filepath.Join(root, layout.src), ref))

				// Worktree content, modes and mtimes, and every metadata
				// file byte for byte: HEAD, refs, config, normalized index.
				require.Equal(t, withoutObjects(localTreeSnapshot(t, baseline)), withoutObjects(localTreeSnapshot(t, root)))
				for _, args := range [][]string{
					{"for-each-ref", "--format=%(objectname) %(refname) %(symref)"},
					{"rev-parse", "HEAD"},
					{"symbolic-ref", "-q", "HEAD"},
					{"config", "--local", "--list"},
					{"status", "--porcelain", "--ignored"},
				} {
					want, _ := gitutil.NewGitCLI(gitutil.WithDir(baseline)).New(gitutil.WithIgnoreError()).Run(ctx, args...)
					got, _ := gitutil.NewGitCLI(gitutil.WithDir(root)).New(gitutil.WithIgnoreError()).Run(ctx, args...)
					require.Equal(t, string(want), string(got), "git %v", args)
				}
				gitMirrorTestRun(t, root, "fsck", "--strict", "--no-dangling")
				// Nothing was fetched, written or freshened in the object store.
				require.Equal(t, inherited, objectFiles(t, filepath.Join(root, ".git", "objects")))
			})
		}
	}
	// The source's unreachable tags really exist, so the comparison above
	// proved they are not followed.
	require.NotEmpty(t, gitMirrorTestRun(t, source, "tag", "--list", "side-*"))
}

// The delta path moves a clean full checkout of an ancestor to the target by
// its tree delta, fetching only the objects the ancestor lacks, and must end
// exactly where a fresh checkout of the target does.
func TestCowGitCheckoutDelta(t *testing.T) {
	ctx := context.Background()
	source, base, tip := cowTestSource(t)
	modes := gitMirrorTestRun(t, source, "rev-parse", "v0.2^{commit}")
	// Replace a directory by a file, a symlink by a file, delete and add
	// nested paths, on top of the tip.
	gitMirrorTestRun(t, source, "checkout", "--quiet", "--", "file")
	require.NoError(t, os.Remove(filepath.Join(source, "untracked")))
	gitMirrorTestRun(t, source, "rm", "-q", "-r", "nested/deeper", "link")
	require.NoError(t, os.WriteFile(filepath.Join(source, "nested", "deeper"), []byte("now a file"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(source, "link"), []byte("no longer a link"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(source, "added", "deep"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "added", "deep", "file"), []byte("added"), 0644))
	gitMirrorTestRun(t, source, "add", "-A")
	gitMirrorTestRun(t, source, "commit", "-q", "-m", "reshape")
	reshaped := gitMirrorTestRun(t, source, "rev-parse", "HEAD")
	gitMirrorTestRun(t, source, "tag", "-a", "-m", "reshaped", "v0.4")

	for _, tc := range []struct {
		name string
		base string
		ref  *gitutil.Ref
	}{
		{"branch", base, &gitutil.Ref{Name: "refs/heads/main", SHA: reshaped}},
		{"detached", base, &gitutil.Ref{SHA: tip}},
		{"commit name", modes, &gitutil.Ref{Name: reshaped, SHA: reshaped}},
		{"tag", base, &gitutil.Ref{Name: "refs/tags/v0.2", SHA: modes}},
		{"other branch", base, &gitutil.Ref{Name: "refs/heads/side", SHA: gitMirrorTestRun(t, source, "rev-parse", "side")}},
		{"reverse shape", tip, &gitutil.Ref{SHA: reshaped}},
		{"unchanged", reshaped, &gitutil.Ref{Name: "refs/heads/main", SHA: reshaped}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseCheckout := freshRetainedCheckout(t, source, &gitutil.Ref{SHA: tc.base})
			want := freshRetainedCheckout(t, source, tc.ref)
			root := cowCopy(t, baseCheckout)
			inherited := objectFiles(t, filepath.Join(root, ".git", "objects"))
			require.NoError(t, cowDeltaCheckout(ctx, root, source, tc.base, tc.ref))

			require.Equal(t, withoutObjects(localTreeSnapshot(t, want)), withoutObjects(localTreeSnapshot(t, root)))
			for _, args := range [][]string{
				{"for-each-ref", "--format=%(objectname) %(refname) %(symref)"},
				{"rev-parse", "HEAD"},
				{"symbolic-ref", "-q", "HEAD"},
				{"config", "--local", "--list"},
				{"status", "--porcelain", "--ignored"},
			} {
				wantOut, _ := gitutil.NewGitCLI(gitutil.WithDir(want)).New(gitutil.WithIgnoreError()).Run(ctx, args...)
				got, _ := gitutil.NewGitCLI(gitutil.WithDir(root)).New(gitutil.WithIgnoreError()).Run(ctx, args...)
				require.Equal(t, string(wantOut), string(got), "git %v", args)
			}
			gitMirrorTestRun(t, root, "fsck", "--strict", "--no-dangling")
			// Inherited objects stay untouched; the only new object files
			// are the delta's pack, smaller than the history.
			after := objectFiles(t, filepath.Join(root, ".git", "objects"))
			for path, id := range inherited {
				require.Equal(t, id, after[path], "inherited object file %s changed", path)
			}
			// Only what the base lacks crossed over: ref's new history and
			// the new tags' objects (the base checkout's HEAD is detached).
			delta := strings.Count(gitMirrorTestRun(t, source, "rev-list", "--objects", tc.ref.SHA, "--not", tc.base)+"\n", "\n")
			received := 0
			for path := range after {
				if _, ok := inherited[path]; !ok {
					require.Contains(t, path, string(filepath.Separator)+"pack"+string(filepath.Separator), "unpacked object %s", path)
					if strings.HasSuffix(path, ".idx") {
						idx, err := os.Open(path)
						require.NoError(t, err)
						cmd := exec.Command("git", "show-index")
						cmd.Dir = root
						cmd.Stdin = idx
						out, err := cmd.Output()
						idx.Close()
						require.NoError(t, err)
						received += strings.Count(string(out), "\n")
					}
				}
			}
			require.LessOrEqual(t, received, delta+4, "fetched more than the delta's objects")
		})
	}

	t.Run("base mismatch", func(t *testing.T) {
		root := cowCopy(t, freshRetainedCheckout(t, source, &gitutil.Ref{SHA: base}))
		before := historySnapshot(t, root)
		err := cowDeltaCheckout(ctx, root, source, tip, &gitutil.Ref{SHA: reshaped})
		var reason nativeCommitUnsupportedReason
		require.ErrorAs(t, err, &reason)
		require.Equal(t, "base-head", string(reason))
		require.Equal(t, before, historySnapshot(t, root))
	})
}

// A copy-on-write checkout inherits its parent snapshot's root directory; a
// fetched one writes into a fresh snapshot's. The root is made to match.
func TestCowCheckoutSnapshotRoot(t *testing.T) {
	fresh := t.TempDir()
	require.NoError(t, os.Chmod(fresh, 0755))
	want, err := readSnapshotRootInfo(fresh)
	require.NoError(t, err)
	inherited := t.TempDir()
	require.NoError(t, os.Chmod(inherited, 0o2751))
	require.NoError(t, setSnapshotRootInfo(inherited, want))
	got, err := readSnapshotRootInfo(inherited)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// BenchmarkCowGitCheckout compares the full checkout's fetch into an empty
// directory with the copy-on-write checkout, on a synthetic history:
//
//	go test ./core -run '^$' -bench CowGitCheckout -benchtime 3x
func BenchmarkCowGitCheckout(b *testing.B) {
	const files, commits, changes = 3000, 5000, 5
	source := b.TempDir()
	gitMirrorTestRun(b, source, "init", "--quiet", "--initial-branch=main")
	var stream strings.Builder
	content := func(file, rev int) string {
		line := fmt.Sprintf("file %d revision %d\n", file, rev)
		return strings.Repeat(line, 2048/len(line))
	}
	blob := func(path, data string) {
		fmt.Fprintf(&stream, "M 100644 inline %s\ndata %d\n%s\n", path, len(data), data)
	}
	for c := range commits {
		fmt.Fprintf(&stream, "commit refs/heads/main\ncommitter Dagger <dagger@localhost> %d +0000\ndata 7\ncommit\n", 1_000_000_000+c)
		if c == 0 {
			for f := range files {
				blob(fmt.Sprintf("dir%d/file%d", f%30, f), content(f, 0))
			}
		} else {
			for i := range changes {
				f := (c*changes + i) * 7919 % files
				blob(fmt.Sprintf("dir%d/file%d", f%30, f), content(f, c))
			}
		}
		stream.WriteString("\n")
	}
	git := gitutil.NewGitCLI(gitutil.WithDir(source))
	_, err := git.RunWithStdin(b.Context(), strings.NewReader(stream.String()), "fast-import", "--quiet")
	require.NoError(b, err)
	gitMirrorTestRun(b, source, "repack", "-a", "-d", "--quiet")
	gitMirrorTestRun(b, source, "checkout", "--quiet", "main")
	tip := gitMirrorTestRun(b, source, "rev-parse", "HEAD")
	ref := &gitutil.Ref{Name: "refs/heads/main", SHA: tip}

	b.Run("fetch", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()
			dest := b.TempDir()
			b.StartTimer()
			require.NoError(b, doGitCheckout(b.Context(), localTreeCheckoutCLI(dest), nil, "file://"+filepath.Join(source, ".git"), ref, 0, false))
		}
	})
	b.Run("cow", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()
			root := cowCopy(b, source)
			b.StartTimer()
			require.NoError(b, cowWipeCheckout(b.Context(), root, root, ref))
		}
	})
}

func TestCowGitCheckoutFallback(t *testing.T) {
	ctx := context.Background()
	source, _, tip := cowTestSource(t)
	ref := &gitutil.Ref{Name: "refs/heads/main", SHA: tip}

	linked := t.TempDir()
	gitMirrorTestRun(t, source, "worktree", "add", "--detach", filepath.Join(linked, "worktree"), tip)
	shared := t.TempDir()
	gitMirrorTestRun(t, shared, "clone", "--quiet", "--shared", source, ".")
	shallow := t.TempDir()
	gitMirrorTestRun(t, shallow, "clone", "--quiet", "--depth=1", "file://"+source, ".")
	bare := t.TempDir()
	gitMirrorTestRun(t, bare, "clone", "--quiet", "--bare", source, ".")
	sha256Repo := historyRepo(t, "sha256")
	sha256Tip := historyCommit(t, sha256Repo, "file", "content")
	reftable := t.TempDir()
	var reftableTip string
	if exec.Command("git", "init", "--quiet", "--ref-format=reftable", reftable).Run() == nil {
		gitMirrorTestRun(t, reftable, "config", "user.name", "History Author")
		gitMirrorTestRun(t, reftable, "config", "user.email", "history@example.com")
		reftableTip = historyCommit(t, reftable, "file", "content")
	}

	for _, tc := range []struct {
		name   string
		root   string
		src    string
		ref    *gitutil.Ref
		mutate func(t *testing.T, root string)
		reason string
	}{
		{name: "linked worktree", root: linked, src: "worktree", reason: "git-directory-layout"},
		{name: "alternates", root: shared, reason: "object-alternates"},
		{name: "shallow", root: shallow, reason: "shallow-history"},
		{name: "root bare repository", root: bare, reason: "repository-layout"},
		{name: "subdirectory", root: filepath.Dir(source), src: filepath.Base(source), reason: "repository-layout"},
		{name: "sha256", root: sha256Repo, ref: &gitutil.Ref{SHA: sha256Tip}, reason: "object-format"},
		{name: "promisor pack", root: source, reason: "partial-repository", mutate: func(t *testing.T, root string) {
			packs, err := filepath.Glob(filepath.Join(root, ".git", "objects", "pack", "*.pack"))
			require.NoError(t, err)
			require.NotEmpty(t, packs)
			require.NoError(t, os.WriteFile(strings.TrimSuffix(packs[0], ".pack")+".promisor", nil, 0644))
		}},
		{name: "partial clone config", root: source, reason: "partial-repository", mutate: func(t *testing.T, root string) {
			gitMirrorTestRun(t, root, "config", "remote.origin.promisor", "true")
		}},
		{name: "http alternates", root: source, reason: "object-alternates", mutate: func(t *testing.T, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "objects", "info", "http-alternates"), []byte("https://example.com/objects\n"), 0644))
		}},
		{name: "grafts", root: source, reason: "grafts", mutate: func(t *testing.T, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "info", "grafts"), []byte(tip+"\n"), 0644))
		}},
		{name: "hidden refs", root: source, reason: "hidden-refs", mutate: func(t *testing.T, root string) {
			gitMirrorTestRun(t, root, "config", "uploadpack.hideRefs", "refs/tags/")
		}},
		{name: "tag to tree", root: source, reason: "tag-target", mutate: func(t *testing.T, root string) {
			gitMirrorTestRun(t, root, "tag", "tree", tip+"^{tree}")
		}},
		{name: "reftable", root: reftable, ref: &gitutil.Ref{SHA: reftableTip}, reason: "ref-storage", mutate: func(t *testing.T, root string) {
			if reftableTip == "" {
				t.Skip("git does not support reftable")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := ref
			if tc.ref != nil {
				ref = tc.ref
			}
			root := cowCopy(t, tc.root)
			if tc.mutate != nil {
				tc.mutate(t, root)
			}
			before := historySnapshot(t, root)
			historyForbidFetch(t)
			err := cowWipeCheckout(ctx, root, filepath.Join(root, tc.src), ref)
			var reason nativeCommitUnsupportedReason
			require.ErrorAs(t, err, &reason)
			require.Equal(t, tc.reason, string(reason))
			require.True(t, nativeCommitFallback(err), "%v", err)
			require.Equal(t, before, historySnapshot(t, root), "an unsupported source is left untouched")
		})
	}

	t.Run("missing commit", func(t *testing.T) {
		root := cowCopy(t, source)
		before := historySnapshot(t, root)
		err := cowWipeCheckout(ctx, root, root, &gitutil.Ref{SHA: "1234567890123456789012345678901234567890"})
		require.Error(t, err)
		require.False(t, errors.Is(err, context.Canceled))
		require.True(t, nativeFallback(ctx, trace.SpanFromContext(ctx), "fallback_reason", err))
		require.Equal(t, before, historySnapshot(t, root))
	})
}
