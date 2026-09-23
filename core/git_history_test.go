package core

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

// Embed the unused Tree method; mounting deliberately has no dagql recipe.
// The single-ref test therefore also proves that RecipeDigest is not consulted.
type historyTestRef struct {
	GitRefBackend
	git  *gitutil.GitCLI
	held bool
	err  error
}

func (ref *historyTestRef) mount(ctx context.Context, depth int, tags bool, fn func(*gitutil.GitCLI) error) error {
	if ref.err != nil {
		return ref.err
	}
	ref.held = true
	defer func() { ref.held = false }()
	return fn(ref.git)
}

func historyRef(dir, sha string) *GitRef {
	return &GitRef{Ref: &gitutil.Ref{SHA: sha}, Backend: &historyTestRef{git: gitutil.NewGitCLI(gitutil.WithDir(dir))}}
}

func historyRepo(t *testing.T, format string) string {
	t.Helper()
	dir := t.TempDir()
	gitMirrorTestRun(t, dir, "init", "--initial-branch=main", "--object-format="+format)
	gitMirrorTestRun(t, dir, "config", "user.name", "History Author")
	gitMirrorTestRun(t, dir, "config", "user.email", "history@example.com")
	return dir
}

func historyCommit(t *testing.T, dir, path, message string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(message), 0600))
	gitMirrorTestRun(t, dir, "add", "--", path)
	gitMirrorTestRun(t, dir, "commit", "-m", message)
	return gitMirrorTestRun(t, dir, "rev-parse", "HEAD")
}

func historySnapshot(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	files := map[string][32]byte{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil {
			files[path] = sha256.Sum256(data)
		}
		return err
	}))
	return files
}

// Intercept every Git invocation, including the private object's CLI, not just
// commands issued through a source mount. A fetch regression fails immediately.
func historyForbidFetch(t *testing.T) {
	t.Helper()
	git, err := exec.LookPath("git")
	require.NoError(t, err)
	bin := t.TempDir()
	script := "#!/bin/sh\nfor arg do\n case \"$arg\" in fetch|clone|repack|pack-objects) echo 'object transfer forbidden' >&2; exit 99;; esac\ndone\nexec '" + strings.ReplaceAll(git, "'", "'\\''") + "' \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCachedGitHistory(t *testing.T) {
	ctx := context.Background()
	source := historyRepo(t, "sha1")
	root := historyCommit(t, source, "root", "root")
	base := historyCommit(t, source, "other", "base")
	before := t.TempDir()
	gitMirrorTestRun(t, before, "clone", "--no-hardlinks", source, ".")
	next := historyCommit(t, source, "changed", "next\n\nCommit body.")
	fork := t.TempDir()
	gitMirrorTestRun(t, fork, "clone", "--no-hardlinks", before, ".")
	gitMirrorTestRun(t, fork, "config", "user.name", "History Author")
	gitMirrorTestRun(t, fork, "config", "user.email", "history@example.com")
	diverged := historyCommit(t, fork, "fork", "diverged")
	unrelated := historyRepo(t, "sha1")
	other := historyCommit(t, unrelated, "unrelated", "unrelated")
	gitMirrorTestRun(t, source, "fetch", fork, diverged)
	gitMirrorTestRun(t, source, "merge", "--no-ff", "-m", "merge", "FETCH_HEAD")
	merged := gitMirrorTestRun(t, source, "rev-parse", "HEAD")

	// Use a linked worktree with an awkward path, whose object database lives
	// in the common dir, not the per-worktree gitdir. Quote that path too.
	common := filepath.Join(t.TempDir(), "common :\"\\\n repo")
	require.NoError(t, os.Rename(source, common))
	source = common
	worktree := filepath.Join(t.TempDir(), "linked worktree")
	gitMirrorTestRun(t, source, "worktree", "add", "--detach", worktree, next)
	gitMirrorTestRun(t, source, "gc") // Exercise borrowed packfiles as well as loose objects.
	shared := t.TempDir()
	gitMirrorTestRun(t, shared, "clone", "--shared", fork, ".")

	inputs := []string{source, before, fork, unrelated, worktree, shared}
	snapshots := make([]map[string][32]byte, len(inputs))
	for i, input := range inputs {
		snapshots[i] = historySnapshot(t, input)
	}
	historyForbidFetch(t)

	for _, tc := range []struct {
		name                       string
		tipDir, tip, baseDir, base string
		paths                      []string
		want                       []string
		mergeBase                  string
	}{
		{name: "forward", tipDir: source, tip: next, baseDir: before, base: base, want: []string{next}, mergeBase: base},
		{name: "reverse", tipDir: before, tip: base, baseDir: source, base: next, mergeBase: base},
		{name: "diverged", tipDir: source, tip: next, baseDir: fork, base: diverged, want: []string{next}, mergeBase: base},
		{name: "diverged reverse", tipDir: fork, tip: diverged, baseDir: source, base: next, want: []string{diverged}, mergeBase: base},
		{name: "unrelated", tipDir: source, tip: next, baseDir: unrelated, base: other, want: []string{next, base, root}},
		{name: "path matches", tipDir: source, tip: next, baseDir: before, base: root, paths: []string{"changed"}, want: []string{next}, mergeBase: root},
		{name: "path excluded", tipDir: source, tip: next, baseDir: before, base: base, paths: []string{"other"}, mergeBase: base},
		{name: "worktree", tipDir: worktree, tip: next, baseDir: before, base: base, want: []string{next}, mergeBase: base},
		{name: "merge", tipDir: source, tip: merged, baseDir: fork, base: diverged, want: []string{merged, next}, mergeBase: diverged},
		{name: "existing alternates", tipDir: shared, tip: diverged, baseDir: source, base: next, want: []string{diverged}, mergeBase: base},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs := []*GitRef{historyRef(tc.tipDir, tc.tip), historyRef(tc.baseDir, tc.base)}
			var viewDir string
			require.NoError(t, mountCachedGitRefs(ctx, refs, func(git *gitutil.GitCLI, shas []string) error {
				viewDir = git.Dir()
				for _, ref := range refs {
					require.True(t, ref.Backend.(*historyTestRef).held)
				}
				args := []string{"rev-list", shas[0], "^" + shas[1]}
				if len(tc.paths) > 0 {
					args = append(append(args, "--"), tc.paths...)
				}
				out, err := git.Run(ctx, args...)
				require.NoError(t, err)
				got := strings.Fields(string(out))
				for _, sha := range got {
					// Read metadata via the public method while the view and its
					// borrowed object databases are still mounted.
					ref := &GitRef{Ref: &gitutil.Ref{SHA: sha}, Backend: &historyTestRef{git: git}}
					commits, err := ref.Log(ctx, GitLogOptions{Limit: 1})
					require.NoError(t, err)
					require.Len(t, commits, 1)
					commit := commits[0]
					require.Equal(t, "History Author", commit.AuthorName)
					require.Equal(t, "history@example.com", commit.CommitterEmail)
					if commit.SHA == next {
						require.Equal(t, "next\n\nCommit body.", commit.Message)
						require.Equal(t, []string{base}, commit.ParentSHAs)
					}
				}
				if len(tc.want) == 0 {
					require.Empty(t, got)
				} else {
					require.Equal(t, tc.want, got)
				}
				out, err = git.Run(ctx, "merge-base", shas[0], shas[1])
				if tc.mergeBase == "" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.Equal(t, tc.mergeBase, strings.TrimSpace(string(out)))
				}
				// The private view contains an alternates file, no object copies.
				objects := historySnapshot(t, filepath.Join(viewDir, "objects"))
				require.Len(t, objects, 1)
				_, ok := objects[filepath.Join(viewDir, "objects", "info", "alternates")]
				require.True(t, ok)
				return nil
			}))
			_, err := os.Stat(viewDir)
			require.True(t, os.IsNotExist(err))
			for _, ref := range refs {
				require.False(t, ref.Backend.(*historyTestRef).held)
			}
		})
	}
	for i, input := range inputs {
		require.Equal(t, snapshots[i], historySnapshot(t, input), "input repository changed: %s", input)
	}
}

func TestCachedGitHistoryCleanup(t *testing.T) {
	dir := historyRepo(t, "sha1")
	sha := historyCommit(t, dir, "file", "commit")
	for _, cancel := range []bool{false, true} {
		ctx, stop := context.WithCancel(context.Background())
		refs := []*GitRef{historyRef(dir, sha), historyRef(dir, sha)}
		var viewDir string
		failure := errors.New("callback failed")
		err := mountCachedGitRefs(ctx, refs, func(git *gitutil.GitCLI, shas []string) error {
			viewDir = git.Dir()
			if cancel {
				stop()
				_, err := git.Run(ctx, "rev-list", shas[0])
				return err
			}
			return failure
		})
		stop()
		require.Error(t, err)
		if !cancel {
			require.ErrorIs(t, err, failure)
		}
		_, err = os.Stat(viewDir)
		require.True(t, os.IsNotExist(err))
		for _, ref := range refs {
			require.False(t, ref.Backend.(*historyTestRef).held)
		}
	}
	refs := []*GitRef{historyRef(dir, sha), historyRef(dir, sha)}
	failure := errors.New("mount failed")
	refs[1].Backend.(*historyTestRef).err = failure
	require.ErrorIs(t, mountCachedGitRefs(context.Background(), refs, func(*gitutil.GitCLI, []string) error {
		t.Fatal("unexpected callback")
		return nil
	}), failure)
	require.False(t, refs[0].Backend.(*historyTestRef).held)
}

func TestCachedGitHistoryShallowAndObjectFormats(t *testing.T) {
	sha1 := historyRepo(t, "sha1")
	base := historyCommit(t, sha1, "file", "base")
	next := historyCommit(t, sha1, "file", "next")
	shallow := t.TempDir()
	gitMirrorTestRun(t, shallow, "clone", "--depth=1", "file://"+sha1, ".")
	sha256 := historyRepo(t, "sha256")
	other := historyCommit(t, sha256, "file", "other")
	historyForbidFetch(t)
	for _, refs := range [][]*GitRef{
		{historyRef(sha1, base), historyRef(shallow, next)},
		{historyRef(shallow, next), historyRef(sha1, base)},
	} {
		err := mountCachedGitRefs(context.Background(), refs, func(*gitutil.GitCLI, []string) error {
			t.Fatal("must use fallback rather than ignore or union shallow boundaries")
			return nil
		})
		require.ErrorIs(t, err, errShallowCachedGitHistory)
		for _, ref := range refs {
			require.False(t, ref.Backend.(*historyTestRef).held)
		}
	}
	err := mountCachedGitRefs(context.Background(), []*GitRef{historyRef(sha1, next), historyRef(sha256, other)}, func(*gitutil.GitCLI, []string) error {
		t.Fatal("mixed formats must be rejected")
		return nil
	})
	require.ErrorContains(t, err, "different object formats")
	require.NoError(t, mountCachedGitRefs(context.Background(), []*GitRef{historyRef(sha256, other), historyRef(sha256, other)}, func(git *gitutil.GitCLI, shas []string) error {
		ref := &GitRef{Ref: &gitutil.Ref{SHA: shas[0]}, Backend: &historyTestRef{git: git}}
		commits, err := ref.Log(context.Background(), GitLogOptions{Limit: 1})
		require.NoError(t, err)
		require.Len(t, commits, 1)
		require.Equal(t, other, commits[0].SHA)
		return nil
	}))
}

func TestGitLogSingleRefWithoutRecipe(t *testing.T) {
	dir := historyRepo(t, "sha1")
	base := historyCommit(t, dir, "file", "base")
	next := historyCommit(t, dir, "other", "next")
	historyForbidFetch(t)
	ref := historyRef(dir, next)
	commits, err := ref.Log(context.Background(), GitLogOptions{Limit: 1, Paths: []string{"file"}})
	require.NoError(t, err)
	require.Len(t, commits, 1)
	require.Equal(t, base, commits[0].SHA)
	require.False(t, ref.Backend.(*historyTestRef).held)
}
