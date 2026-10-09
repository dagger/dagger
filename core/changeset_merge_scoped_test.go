package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// scopedMergeFixture writes the merge base used by the scoped base tests: a
// realistic mix of what `git add -A` must classify, plus a directory with many
// untouched siblings, so a branch commit writes trees over missing blobs.
func scopedMergeFixture(t *testing.T, dir string) {
	t.Helper()
	write := func(p, data string, mode os.FileMode) {
		t.Helper()
		name := filepath.Join(dir, p)
		require.NoError(t, os.MkdirAll(filepath.Dir(name), 0755))
		require.NoError(t, os.WriteFile(name, []byte(data), mode))
		require.NoError(t, os.Chmod(name, mode))
	}
	write(".gitignore", "*.log\n/build/\n!keep.log\n", 0644)
	write("sub/.gitignore", "local-*\n", 0644)
	write(".gitattributes", "*.crlf text eol=crlf\n*.id ident\n", 0644)
	write("edit.txt", "one\ntwo\nthree\nfour\nfive\nsix\nseven\n", 0644)
	write("remove.txt", "remove me\n", 0644)
	write("run.sh", "#!/bin/sh\necho run\n", 0755)
	write("plain.sh", "#!/bin/sh\necho plain\n", 0644)
	write("text.crlf", "line one\nline two\n", 0644)
	write("identity.id", "$Id$\n", 0644)
	write("debug.log", "ignored\n", 0644)
	write("keep.log", "negated ignore\n", 0644)
	write("build/out", "ignored directory\n", 0644)
	write("sub/local-cache", "nested ignore\n", 0644)
	write("sub/tracked", "tracked in sub\n", 0644)
	write(":colon [odd] name", "pathspec magic\n", 0644)
	write("space dir/naïve file", "non-ascii\n", 0644)
	for _, name := range []string{"a", "b", "c"} {
		write("moved/"+name, strings.Repeat(name+" line\n", 20), 0644)
	}
	for _, name := range []string{"x", "y", "z"} {
		write("partial/"+name, strings.Repeat(name+" line\n", 20), 0644)
	}
	for i := range 200 {
		write(fmt.Sprintf("many/f%c-%03d", 'a'+i%26, i), fmt.Sprintf("sibling %03d\n", i), 0644)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "empty/nested"), 0755))
	require.NoError(t, os.Symlink("edit.txt", filepath.Join(dir, "link")))
	require.NoError(t, os.Symlink("moved", filepath.Join(dir, "dirlink")))
	require.NoError(t, os.Symlink("missing-target", filepath.Join(dir, "dangling")))
	// Snapshot contents carry normalized timestamps.
	normalizeScopedMergeTimes(t, dir)
}

func normalizeScopedMergeTimes(t *testing.T, dir string) {
	t.Helper()
	stamp := time.Unix(1, 0)
	require.NoError(t, filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		return os.Chtimes(name, stamp, stamp)
	}))
}

// copyScopedMergeTree copies a directory tree, keeping modes, symlinks and
// modification times.
func copyScopedMergeTree(t *testing.T, src, dst string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(src, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, name)
		if err != nil {
			return err
		}
		if rel == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(name)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.IsDir():
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		default:
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
				return err
			}
			if err := os.Chmod(target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chtimes(target, info.ModTime(), info.ModTime())
		}
	}))
	// Directory times last: writing children bumps them.
	require.NoError(t, filepath.WalkDir(src, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, name)
		if err != nil {
			return err
		}
		if rel == ".git" {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chtimes(filepath.Join(dst, rel), info.ModTime(), info.ModTime())
	}))
}

// scopedMergeContent makes a changeset's content as Changeset.content would:
// paths from ComputePaths' content comparison of base and the edited copy, and
// a diff directory holding the added and modified entries. extra entries are
// copied into the diff without being declared, as the stat-sensitive snapshot
// differ does for metadata-only rewrites.
func scopedMergeContent(t *testing.T, base string, edit func(dir string), extra ...string) *changesetContent {
	t.Helper()
	after := filepath.Join(t.TempDir(), "after")
	copyScopedMergeTree(t, base, after)
	edit(after)
	paths, err := computeChangesetPaths(t.Context(), base, after)
	require.NoError(t, err)
	diff := filepath.Join(t.TempDir(), "diff")
	require.NoError(t, os.Mkdir(diff, 0755))
	for _, p := range slices.Concat(paths.Added, paths.Modified, extra) {
		if strings.HasSuffix(p, "/") {
			continue
		}
		for _, dir := range scopedMergeAncestors(p) {
			info, err := os.Lstat(filepath.Join(after, dir))
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Join(diff, dir), 0755))
			require.NoError(t, os.Chmod(filepath.Join(diff, dir), info.Mode().Perm()))
		}
		src, dst := filepath.Join(after, p), filepath.Join(diff, p)
		info, err := os.Lstat(src)
		require.NoError(t, err)
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(src)
			require.NoError(t, err)
			require.NoError(t, os.Symlink(link, dst))
			continue
		}
		data, err := os.ReadFile(src)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(dst, data, info.Mode().Perm()))
		require.NoError(t, os.Chmod(dst, info.Mode().Perm()))
		require.NoError(t, os.Chtimes(dst, info.ModTime(), info.ModTime()))
	}
	return &changesetContent{paths: paths, localDiff: diff}
}

func scopedMergeAncestors(p string) []string {
	var dirs []string
	for dir := path.Dir(p); dir != "."; dir = path.Dir(dir) {
		dirs = append([]string{dir}, dirs...)
	}
	return dirs
}

func scopedMergeWrite(t *testing.T, dir, p, data string, mode os.FileMode) {
	t.Helper()
	name := filepath.Join(dir, p)
	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0755))
	require.NoError(t, os.RemoveAll(name))
	require.NoError(t, os.WriteFile(name, []byte(data), mode))
	require.NoError(t, os.Chmod(name, mode))
}

func scopedMergeRename(t *testing.T, dir, from, to string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, to)), 0755))
	require.NoError(t, os.Rename(filepath.Join(dir, from), filepath.Join(dir, to)))
}

func scopedMergeTreeObjects(t *testing.T, dir string) (tree string, objects int) {
	t.Helper()
	out, err := runGitOutput(t.Context(), dir, "rev-parse", "HEAD^{tree}")
	require.NoError(t, err)
	count, err := runGitOutput(t.Context(), dir, "count-objects")
	require.NoError(t, err)
	var n, kb int
	_, err = fmt.Sscanf(count, "%d objects, %d kilobytes", &n, &kb)
	require.NoError(t, err)
	return strings.TrimSpace(out), n
}

// The scoped base is the very tree `git add -A` commits, so every merge
// decision (rename and directory rename detection included) is unchanged; it
// only leaves out-of-scope blobs unwritten.
func TestScopedGitMergeBaseTree(t *testing.T) {
	ctx := t.Context()
	base := filepath.Join(t.TempDir(), "base")
	scopedMergeFixture(t, base)
	full, scoped := filepath.Join(t.TempDir(), "full"), filepath.Join(t.TempDir(), "scoped")
	copyScopedMergeTree(t, base, full)
	copyScopedMergeTree(t, base, scoped)
	require.NoError(t, initGitRepo(ctx, full))
	scope := &gitMergeScope{exact: map[string]bool{"edit.txt": true}, dirs: map[string]bool{"moved": true}}
	require.NoError(t, initScopedGitRepo(ctx, scoped, scope))
	fullTree, fullObjects := scopedMergeTreeObjects(t, full)
	scopedTree, scopedObjects := scopedMergeTreeObjects(t, scoped)
	require.Equal(t, fullTree, scopedTree)
	require.Less(t, scopedObjects*4, fullObjects, "out-of-scope blobs must not be written")
	listing, err := runGitOutput(ctx, full, "ls-tree", "-r", "-z", "--full-tree", "HEAD")
	require.NoError(t, err)
	var sawIgnored bool
	for _, entry := range splitOnNul([]byte(listing)) {
		header, name, ok := strings.Cut(entry, "\t")
		require.True(t, ok)
		fields := strings.Fields(header)
		in := scope.inScope(name)
		_, err := runGitOutput(ctx, scoped, "cat-file", "-e", fields[2])
		require.Equal(t, in, err == nil, "%s: object written iff in scope", name)
		if name == "keep.log" {
			sawIgnored = true
		}
		require.NotContains(t, []string{"debug.log", "build/out", "sub/local-cache"}, name)
	}
	require.True(t, sawIgnored, "negated ignore rules must be honored")
	for _, name := range []string{".gitattributes", ".gitignore", "sub/.gitignore", "edit.txt", "moved/a"} {
		require.True(t, scope.inScope(name), name)
	}
	require.False(t, scope.inScope("many/fa-000"))

	// An embedded repository becomes a gitlink under add -A. Leave that to
	// the full base.
	nested := filepath.Join(t.TempDir(), "nested")
	copyScopedMergeTree(t, base, nested)
	_, err = runWorkspaceCommitGit(ctx, filepath.Join(nested, "sub"), nil, "init", "-q")
	require.NoError(t, err)
	err = initScopedGitRepo(ctx, nested, &gitMergeScope{exact: map[string]bool{}, dirs: map[string]bool{}})
	require.ErrorContains(t, err, "embedded repository")
}

func TestScopedGitMergeMatchesFull(t *testing.T) {
	type side = func(t *testing.T, base string) *changesetContent
	edit := func(f func(t *testing.T, dir string), extra ...string) side {
		return func(t *testing.T, base string) *changesetContent {
			return scopedMergeContent(t, base, func(dir string) { f(t, dir) }, extra...)
		}
	}
	const seven = "one\ntwo\nthree\nfour\nfive\nsix\nseven\n"
	editLine := func(from, to string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			scopedMergeWrite(t, dir, "edit.txt", strings.Replace(seven, from, to, 1), 0644)
		}
	}
	remove := func(paths ...string) func(t *testing.T, dir string) {
		return func(t *testing.T, dir string) {
			for _, p := range paths {
				require.NoError(t, os.RemoveAll(filepath.Join(dir, p)))
			}
		}
	}
	noop := edit(func(*testing.T, string) {})
	strategies := []WithChangesetMergeConflict{FailOnConflict, LeaveConflictMarkers, PreferOursOnConflict, PreferTheirsOnConflict}
	for _, tc := range []struct {
		name         string
		ours, theirs side
	}{
		{"disjoint", edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, "many/fa-000", "ours\n", 0644)
			scopedMergeWrite(t, d, "new/deep/ours", "ours\n", 0644)
		}), edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, "many/new", "theirs\n", 0644)
			remove("remove.txt")(t, d)
		})},
		{"same file merges", edit(editLine("one", "ONE")), edit(editLine("seven", "SEVEN"))},
		{"content conflict", edit(editLine("four", "OURS")), edit(editLine("four", "THEIRS"))},
		{"rename and modify", edit(func(t *testing.T, d string) {
			scopedMergeRename(t, d, "edit.txt", "renamed/edit.txt")
		}), edit(editLine("seven", "SEVEN"))},
		{"directory rename with addition", edit(func(t *testing.T, d string) {
			for _, name := range []string{"a", "b", "c"} {
				scopedMergeRename(t, d, "moved/"+name, "dest/"+name)
			}
		}), edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, "moved/added", "theirs addition\n", 0644)
		})},
		{"partial directory rename with addition", edit(func(t *testing.T, d string) {
			// partial/z stays: partial/ was not renamed, only two files.
			for _, name := range []string{"x", "y"} {
				scopedMergeRename(t, d, "partial/"+name, "elsewhere/"+name)
			}
		}), edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, "partial/added", "theirs addition\n", 0644)
		})},
		{"modify delete", edit(editLine("one", "ONE")), edit(remove("edit.txt"))},
		{"delete modify", edit(remove("edit.txt")), edit(editLine("one", "ONE"))},
		{"ignored files", edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, "debug.log", "still ignored\n", 0644)
			scopedMergeWrite(t, d, "keep.log", "negated, tracked\n", 0644)
			scopedMergeWrite(t, d, "fresh.log", "new and ignored\n", 0644)
		}), edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, "build/out", "ignored directory edit\n", 0644)
			scopedMergeWrite(t, d, "sub/tracked", "theirs\n", 0644)
		})},
		{"symlinks and modes", edit(func(t *testing.T, d string) {
			require.NoError(t, os.Remove(filepath.Join(d, "link")))
			require.NoError(t, os.Symlink("remove.txt", filepath.Join(d, "link")))
			require.NoError(t, os.Chmod(filepath.Join(d, "plain.sh"), 0755))
			require.NoError(t, os.Remove(filepath.Join(d, "dirlink")))
			scopedMergeWrite(t, d, "dirlink", "symlink became file\n", 0644)
		}), edit(func(t *testing.T, d string) {
			require.NoError(t, os.Chmod(filepath.Join(d, "run.sh"), 0644))
			require.NoError(t, os.Remove(filepath.Join(d, "remove.txt")))
			require.NoError(t, os.Symlink("edit.txt", filepath.Join(d, "remove.txt")))
		})},
		{"empty directories", edit(func(t *testing.T, d string) {
			require.NoError(t, os.MkdirAll(filepath.Join(d, "added-empty/inner"), 0755))
			remove("empty")(t, d)
		}), edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, "empty/nested/filled", "now a file\n", 0644)
		})},
		{"type replacements", edit(func(t *testing.T, d string) {
			remove("partial")(t, d)
			scopedMergeWrite(t, d, "partial", "directory became file\n", 0644)
		}), edit(func(t *testing.T, d string) {
			remove("remove.txt")(t, d)
			scopedMergeWrite(t, d, "remove.txt/child", "file became directory\n", 0644)
		})},
		{"attributes", edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, "text.crlf", "line one\r\nline two\r\nours\r\n", 0644)
			scopedMergeWrite(t, d, "identity.id", "$Id$\nours\n", 0644)
		}), edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, "new.crlf", "theirs\n", 0644)
		})},
		{"undeclared metadata-only entries", edit(editLine("one", "ONE"), "many/fb-001", "run.sh"), edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, "many/fb-001", "sibling 001\n", 0644) // same bytes, fresh mtime
		}, "many/fc-002")},
		{"odd names", edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, ":colon [odd] name", "ours\n", 0644)
		}), edit(func(t *testing.T, d string) {
			scopedMergeWrite(t, d, "space dir/naïve file", "theirs\n", 0644)
		})},
		{"empty side", noop, edit(editLine("one", "ONE"))},
	} {
		for _, strategy := range strategies {
			t.Run(fmt.Sprintf("%s/strategy%d", tc.name, strategy), func(t *testing.T) {
				ctx := t.Context()
				base := filepath.Join(t.TempDir(), "base")
				scopedMergeFixture(t, base)
				ours, theirs := tc.ours(t, base), tc.theirs(t, base)
				conflicts := ours.paths.CheckConflicts(theirs.paths)
				run := func(scoped bool) (string, error) {
					dir := filepath.Join(t.TempDir(), "work")
					copyScopedMergeTree(t, base, dir)
					var scope *gitMergeScope
					if scoped {
						var err error
						scope, err = newGitMergeScope(ctx, ours, theirs)
						require.NoError(t, err)
					}
					ws := &gitMergeWorkspace{root: dir, dir: "/", workDir: dir}
					err := ws.mergeChangesets(ctx, scope, ours, theirs, conflicts, strategy, nil)
					require.NoError(t, os.RemoveAll(filepath.Join(dir, ".git")))
					return dir, err
				}
				fullDir, fullErr := run(false)
				scopedDir, scopedErr := run(true)
				if fullErr != nil {
					t.Logf("full merge: %v", fullErr)
					require.Error(t, scopedErr)
					require.Equal(t, errors.As(fullErr, new(gitMergeConflictError)), errors.As(scopedErr, new(gitMergeConflictError)), "scoped: %v", scopedErr)
					require.Equal(t, gitMergeErrorConflicts(fullErr), gitMergeErrorConflicts(scopedErr))
					return
				}
				require.NoError(t, scopedErr, "the scoped base must not need a missing object")
				require.Equal(t, nativeWorkspaceFilesystem(t, fullDir), nativeWorkspaceFilesystem(t, scopedDir))
			})
		}
	}
}

// gitMergeErrorConflicts keeps the CONFLICT lines of a merge error: the rest
// names scratch commits.
func gitMergeErrorConflicts(err error) []string {
	var lines []string
	for _, line := range strings.Split(err.Error(), "\n") {
		if _, conflict, ok := strings.Cut(line, "CONFLICT"); ok {
			lines = append(lines, conflict)
		}
	}
	return lines
}
