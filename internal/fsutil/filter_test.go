package fsutil

import (
	"context"
	gofs "io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingFS struct {
	FS
	visited []string
}

func (fs *recordingFS) Walk(ctx context.Context, target string, fn gofs.WalkDirFunc) error {
	return fs.FS.Walk(ctx, target, func(path string, entry gofs.DirEntry, err error) error {
		fs.visited = append(fs.visited, filepath.ToSlash(path))
		return fn(path, entry, err)
	})
}

func TestFilterFSPrunesExcludedDirectoriesByPatternPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		patterns   []string
		wantFiles  []string
		wantPruned []string
		wantWalked []string
	}{
		{
			name:       "later exclude overrides wildcard re-include",
			patterns:   []string{"**", "!**/*.webp", "**/node_modules"},
			wantFiles:  []string{"app/image.webp"},
			wantPruned: []string{"app/node_modules"},
		},
		{
			name:       "later re-include may match excluded subtree",
			patterns:   []string{"**/node_modules", "!**/node_modules/**/*.keep"},
			wantFiles:  []string{"app/image.webp", "app/node_modules/deep/cache.keep", "logs/app.log", "logs/keep.txt"},
			wantWalked: []string{"app/node_modules"},
		},
		{
			name:       "later literal re-include points into excluded subtree",
			patterns:   []string{"app/node_modules", "!app/node_modules/deep"},
			wantFiles:  []string{"app/image.webp", "app/node_modules/deep/cache.keep", "app/node_modules/deep/image.webp", "logs/app.log", "logs/keep.txt"},
			wantWalked: []string{"app/node_modules"},
		},
		{
			name:       "later re-include points elsewhere",
			patterns:   []string{"app/node_modules", "logs", "!logs/keep.txt"},
			wantFiles:  []string{"app/image.webp", "logs/keep.txt"},
			wantPruned: []string{"app/node_modules"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, path := range []string{
				"app/image.webp",
				"app/node_modules/cache.js",
				"app/node_modules/deep/cache.keep",
				"app/node_modules/deep/image.webp",
				"logs/app.log",
				"logs/keep.txt",
			} {
				path = filepath.Join(root, filepath.FromSlash(path))
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, nil, 0o644))
			}

			base, err := NewFS(root)
			require.NoError(t, err)
			recording := &recordingFS{FS: base}
			filtered, err := NewFilterFS(recording, &FilterOpt{ExcludePatterns: test.patterns})
			require.NoError(t, err)

			var files []string
			err = filtered.Walk(context.Background(), "/", func(path string, entry gofs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() {
					files = append(files, filepath.ToSlash(path))
				}
				return nil
			})
			require.NoError(t, err)
			require.ElementsMatch(t, test.wantFiles, files)

			for _, dir := range test.wantPruned {
				require.False(t, walkedBelow(recording.visited, dir), "walked below %q", dir)
			}
			for _, dir := range test.wantWalked {
				require.True(t, walkedBelow(recording.visited, dir), "did not walk below %q", dir)
			}
		})
	}
}

func walkedBelow(paths []string, dir string) bool {
	prefix := dir + "/"
	for _, path := range paths {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
