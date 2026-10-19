package fsutil

import (
	"errors"
	gofs "io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilterFSLiteralIncludeCallbackErrors(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a/b"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a/b/file"), nil, 0o640))
	for _, stop := range []string{"a", "a/b/file"} {
		for _, stopErr := range []error{filepath.SkipAll, errors.New("callback stopped")} {
			var paths [][]string
			var errs []error
			for _, ordinary := range []bool{true, false} {
				base, err := NewFS(root)
				require.NoError(t, err)
				if ordinary {
					base = ordinaryWalkFS{base}
				}
				filtered, err := NewFilterFS(base, &FilterOpt{IncludePatterns: []string{"a/b/file"}})
				require.NoError(t, err)
				var visited []string
				err = filtered.Walk(t.Context(), "", func(path string, _ gofs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					visited = append(visited, path)
					if path == stop {
						return stopErr
					}
					return nil
				})
				paths = append(paths, visited)
				errs = append(errs, err)
			}
			require.Equal(t, errs[0], errs[1])
			require.Equal(t, paths[0], paths[1])
		}
	}
}
