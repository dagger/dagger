//go:build linux

package layercopy

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPathTreeSelectsWhatCopySelects compares SelectedFiles with a real Copy of
// the same tree: a file the walk leaves out of a selection would go missing
// from a read that trusts it.
func TestPathTreeSelectsWhatCopySelects(t *testing.T) {
	t.Parallel()

	files := []string{
		"README.md",
		"go.mod",
		"a/keep.txt",
		"a/skip.go",
		"a/b/keep.txt",
		"a/b/c/deep.go",
		"a/b/c/deep.txt",
		".dagger/modules/one/dagger.json",
		".dagger/modules/one/main.go",
		".dagger/modules/one/internal/x.go",
		".dagger/modules/two/dagger.json",
		".dagger/modules/two/.gitignore",
		"docs/a b/spaced name.txt",
		"docs/[brackets].txt",
		"links/inner/file.txt",
	}
	symlinks := map[string]string{
		"links/dirlink":  "inner",
		"links/filelink": "inner/file.txt",
		"links/gone":     "nowhere",
	}

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	for _, file := range files {
		require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, filepath.Dir(file)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(srcRoot, file), []byte(file), 0o644))
	}
	for link, target := range symlinks {
		require.NoError(t, os.Symlink(target, filepath.Join(srcRoot, link)))
	}
	paths := slices.Clone(files)
	for link := range symlinks {
		paths = append(paths, link)
	}
	tree := NewPathTree(paths)

	for _, tc := range []struct {
		root   string
		filter Filter
	}{
		{"/", Filter{Include: []string{"a/**"}}},
		{"/", Filter{Include: []string{"a"}}},
		{"/", Filter{Include: []string{"a/"}}},
		{"/", Filter{Include: []string{"a/*"}}},
		{"/", Filter{Include: []string{"a/*/keep.txt"}}},
		{"/", Filter{Include: []string{"**/*.go"}}},
		{"/", Filter{Include: []string{"*"}}},
		{"/", Filter{Include: []string{"./go.mod"}}},
		{"/", Filter{Include: []string{".dagger/modules/one/dagger.json", ".dagger/modules/one", "go.mod"}}},
		{"/", Filter{Include: []string{".dagger/modules/one/**", "!**/*.go"}}},
		{"/", Filter{Include: []string{"**/*.txt", "!a/**", "a/b/c/deep.txt"}}},
		{"/", Filter{Exclude: []string{"a", "docs"}}},
		{"/", Filter{Exclude: []string{"**/*.go"}}},
		{"/", Filter{Include: []string{"a/**"}, Exclude: []string{"a/b/**"}}},
		{"/", Filter{Include: []string{"a/b"}, Exclude: []string{"a/b/**"}}},
		{"/", Filter{Include: []string{"docs/[[]brackets].txt"}}},
		{"/", Filter{Include: []string{"docs/a b/*"}}},
		{"/", Filter{Include: []string{"links/*"}}},
		{"/", Filter{Include: []string{"links/dirlink"}}},
		{"/", Filter{Include: []string{"links/dirlink/file.txt"}}},
		{"/", Filter{Include: []string{"nothing"}}},
		{"/a", Filter{Include: []string{"keep.txt"}}},
		{"/a", Filter{Include: []string{"b/**"}}},
		{"/a/b", Filter{Exclude: []string{"c"}}},
		{"/.dagger/modules/one", Filter{Include: []string{"dagger.json"}}},
		{"/.dagger/modules", Filter{Include: []string{"*/dagger.json"}}},
		// Globs after a literal directory, as a Go module's sources select
		// packages of the repository's root module.
		{"/", Filter{Include: []string{"go.mod", "a/b/c/*.txt", "a/*.go"}, Exclude: []string{"**/.git", "**/*_test.go"}}},
		{"/", Filter{Include: []string{".dagger/modules/*/dagger.json", "docs/a b/*.txt"}}},
		{"/", Filter{Include: []string{"./a/b/*", "a/../go.mod"}}},
		{"/", Filter{Include: []string{"a/b/c/*.go", "!a/b/c/deep.go", "docs/*"}}},
		{"/", Filter{Include: []string{"a/*/c/*.txt"}}},
		{"/", Filter{Include: []string{"links/*/file.txt", "links/inner/*.txt"}}},
		{"/", Filter{Include: []string{"."}}},
		{"/", Filter{Include: []string{"/a/*.txt"}}},
		{"/a", Filter{Include: []string{"b/c/*.go"}}},
	} {
		dstRoot := filepath.Join(t.TempDir(), "dst")
		require.NoError(t, os.Mkdir(dstRoot, 0o755))
		copier, err := NewCopier(Mount{Root: dstRoot})
		require.NoError(t, err)
		require.NoError(t, copier.Copy(context.Background(), Mount{Root: srcRoot}, tc.root, "/out", CopyOptions{
			Filter:          tc.filter,
			CopyDirContents: true,
			ReplaceExisting: true,
		}))
		require.NoError(t, copier.Close())

		var copied []string
		outRoot := filepath.Join(dstRoot, "out")
		if _, err := os.Lstat(outRoot); err == nil {
			require.NoError(t, filepath.WalkDir(outRoot, func(p string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				rel, err := filepath.Rel(outRoot, p)
				if err != nil {
					return err
				}
				copied = append(copied, strings.TrimPrefix(filepath.Join(tc.root, rel), "/"))
				return nil
			}))
		}
		slices.Sort(copied)

		selected, err := tree.SelectedFiles(tc.root, tc.filter)
		require.NoError(t, err)
		require.Equal(t, copied, selected, "root %s, filter %+v", tc.root, tc.filter)
	}
}

func TestPathTreeIsDir(t *testing.T) {
	t.Parallel()

	tree := NewPathTree([]string{"a/b/file.txt", "top.txt"})
	for _, dir := range []string{"", ".", "/", "a", "/a", "a/b", "a/b/"} {
		require.True(t, tree.IsDir(dir), dir)
	}
	for _, notDir := range []string{"top.txt", "a/b/file.txt", "a/c", "missing"} {
		require.False(t, tree.IsDir(notDir), notDir)
	}
	require.Equal(t, 2, tree.FileCount(""))
	require.Equal(t, 1, tree.FileCount("a"))
	require.Equal(t, 1, tree.FileCount("/a/b/"))
	require.Equal(t, 0, tree.FileCount("missing"))
}
