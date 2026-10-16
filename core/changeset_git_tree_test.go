package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

// Git's tree diff must list exactly what walking the two checkouts lists:
// files, directories (with a trailing "/"), mode-only and type changes, and
// directories replaced by files and the reverse.
func TestGitTreeChangesetPathsMatchWalk(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			ctx := t.Context()
			repo := historyRepo(t, format)
			write := func(rel, contents string, mode os.FileMode) {
				p := filepath.Join(repo, rel)
				require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
				require.NoError(t, os.WriteFile(p, []byte(contents), mode))
				require.NoError(t, os.Chmod(p, mode))
			}
			commit := func(message string) string {
				gitMirrorTestRun(t, repo, "add", "-A")
				gitMirrorTestRun(t, repo, "commit", "--allow-empty", "-m", message)
				return gitMirrorTestRun(t, repo, "rev-parse", "HEAD")
			}
			reset := func() {
				entries, err := os.ReadDir(repo)
				require.NoError(t, err)
				for _, entry := range entries {
					if entry.Name() != ".git" {
						require.NoError(t, os.RemoveAll(filepath.Join(repo, entry.Name())))
					}
				}
			}
			base := func() {
				reset()
				write("same", "same\n", 0o644)
				write("edited", "old\n", 0o644)
				write("mode", "#!/bin/sh\n", 0o644)
				write("dir/kept", "kept\n", 0o644)
				write("dir/sub/removed", "removed\n", 0o644)
				write("dir/sub/deeper/removed", "removed\n", 0o644)
				write("becomes-dir", "file\n", 0o644)
				write("becomes-file/inner/child", "child\n", 0o644)
				write("becomes-link", "file\n", 0o644)
				require.NoError(t, os.Symlink("same", filepath.Join(repo, "link")))
			}
			base()
			first := commit("base")
			reset()
			write("same", "same\n", 0o644)
			write("edited", "new\n", 0o644)
			write("mode", "#!/bin/sh\n", 0o755)
			write("dir/kept", "kept\n", 0o644)
			write("added/deep/file", "added\n", 0o644)
			write("added/top", "added\n", 0o644)
			write("becomes-dir/child", "child\n", 0o644)
			write("becomes-file", "file\n", 0o644)
			require.NoError(t, os.Symlink("same", filepath.Join(repo, "becomes-link")))
			require.NoError(t, os.Symlink("edited", filepath.Join(repo, "link")))
			second := commit("changes")
			base()
			reverted := commit("revert")
			empty := gitMirrorTestRun(t, repo, "hash-object", "-t", "tree", "--stdin")

			treeOf := func(commit string) string {
				return gitMirrorTestRun(t, repo, "rev-parse", commit+"^{tree}")
			}
			require.Equal(t, treeOf(first), treeOf(reverted), "same tree, different commits")
			require.NotEqual(t, first, reverted)

			checkout := func(tree string) string {
				dir := t.TempDir()
				if tree == empty {
					return dir
				}
				git := gitutil.NewGitCLI(gitutil.WithDir(repo), gitutil.WithWorkTree(dir), gitutil.WithIndexFile(filepath.Join(t.TempDir(), "index")))
				_, err := git.Run(ctx, "checkout", tree, "--", ".")
				require.NoError(t, err)
				return dir
			}
			for _, tc := range []struct {
				name          string
				before, after string
			}{
				{"forward", treeOf(first), treeOf(second)},
				{"backward", treeOf(second), treeOf(first)},
				{"root", empty, treeOf(first)},
				{"to empty", treeOf(second), empty},
				{"same tree", treeOf(first), treeOf(reverted)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					out, err := gitutil.NewGitCLI(gitutil.WithDir(repo)).Run(ctx, gitTreeDiffArgs(tc.before, tc.after)...)
					require.NoError(t, err)
					got, err := parseGitTreeChangesetPaths(out)
					require.NoError(t, err)
					want, err := computeChangesetPaths(ctx, checkout(tc.before), checkout(tc.after))
					require.NoError(t, err)
					// Renames are left out by design: they are also listed as
					// an addition and a removal, which is all callers use.
					require.ElementsMatch(t, want.Added, got.Added, "Added")
					require.ElementsMatch(t, want.Modified, got.Modified, "Modified")
					require.ElementsMatch(t, want.Removed, got.Removed, "Removed")
					require.ElementsMatch(t, want.AllRemoved, got.AllRemoved, "AllRemoved")
					wantEmpty := len(want.Added)+len(want.Modified)+len(want.AllRemoved) == 0
					require.Equal(t, wantEmpty, !changesetPathsTouchFiles(got))
				})
			}

			out, err := gitutil.NewGitCLI(gitutil.WithDir(repo)).Run(ctx, gitTreeDiffArgs(treeOf(first), treeOf(second))...)
			require.NoError(t, err)
			got, err := parseGitTreeChangesetPaths(out)
			require.NoError(t, err)
			// A file replaced by a symlink is a type change: a modification.
			require.Equal(t, []string{"added/", "added/deep/", "added/deep/file", "added/top", "becomes-dir/", "becomes-dir/child", "becomes-file"}, got.Added)
			require.Equal(t, []string{"becomes-link", "edited", "link", "mode"}, got.Modified)
			require.Equal(t, []string{"becomes-dir", "becomes-file/", "dir/sub/"}, got.Removed)
		})
	}
}

func TestParseGitTreeChangesetPathsFallbacks(t *testing.T) {
	sha := strings.Repeat("a", 40)
	zero := strings.Repeat("0", 40)
	entry := func(src, dst, status, p string) []byte {
		return []byte(":" + src + " " + dst + " " + zero + " " + sha + " " + status + "\x00" + p + "\x00")
	}
	for _, tc := range []struct {
		name   string
		diff   []byte
		reason string
	}{
		{"gitlink", entry("000000", "160000", "A", "vendor/module"), "gitlink-change"},
		{"gitlink type change", entry("100644", "160000", "T", "module"), "gitlink-change"},
		{"attributes", entry("100644", "100644", "M", "sub/.gitattributes"), "checkout-controls"},
		{"modules", entry("000000", "100644", "A", ".gitmodules"), "checkout-controls"},
		{"git metadata", entry("000000", "100644", "A", ".git/config"), "unsafe-path"},
		{"traversal", entry("000000", "100644", "A", "../escape"), "unsafe-path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseGitTreeChangesetPaths(tc.diff)
			require.Error(t, err)
			require.True(t, nativeCommitFallback(err))
			require.Equal(t, tc.reason, err.Error())
		})
	}
	_, err := parseGitTreeChangesetPaths(entry("100644", "100644", "R100", "renamed"))
	require.ErrorContains(t, err, "unexpected git diff status")
	_, err = parseGitTreeChangesetPaths([]byte(":100644"))
	require.ErrorContains(t, err, "invalid git tree diff")
}
