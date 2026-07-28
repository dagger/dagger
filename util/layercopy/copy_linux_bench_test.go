//go:build linux

package layercopy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildBenchTree writes a tree approximating a source repository: a fixed
// branching factor down to the given depth, with filesPerDir regular files at
// every level. Depth matters as much as file count here, because destination
// path resolution walks from the root for each entry.
func buildBenchTree(tb testing.TB, root string, depth, branch, filesPerDir int) int {
	tb.Helper()
	files := 0
	var build func(dir string, d int)
	build = func(dir string, d int) {
		require.NoError(tb, os.MkdirAll(dir, 0o755))
		for i := range filesPerDir {
			p := filepath.Join(dir, fmt.Sprintf("file%d.txt", i))
			require.NoError(tb, os.WriteFile(p, []byte("contents"), 0o644))
			files++
		}
		if d == 0 {
			return
		}
		for i := range branch {
			build(filepath.Join(dir, fmt.Sprintf("dir%d", i)), d-1)
		}
	}
	build(root, depth)
	return files
}

// BenchmarkCopyDirectoryOntoExisting mirrors the engine's hot path: repeated
// Directory.withDirectory merges of the same large source tree onto a
// destination that already contains it, i.e. CopyDirContents+ReplaceExisting
// where every entry already exists at the destination.
func BenchmarkCopyDirectoryOntoExisting(b *testing.B) {
	root := b.TempDir()
	srcRoot := filepath.Join(root, "src")
	dstRoot := filepath.Join(root, "dst")
	files := buildBenchTree(b, srcRoot, 6, 2, 16)
	require.NoError(b, os.Mkdir(dstRoot, 0o755))

	opts := CopyOptions{CopyDirContents: true, ReplaceExisting: true}
	ctx := context.Background()

	// Seed the destination so every measured iteration takes the
	// ReplaceExisting path, as it does for the second and later merges.
	seed, err := NewCopier(Mount{Root: dstRoot})
	require.NoError(b, err)
	require.NoError(b, seed.Copy(ctx, Mount{Root: srcRoot}, "/", "/", opts))
	require.NoError(b, seed.Close())

	b.ReportMetric(float64(files), "files")
	for b.Loop() {
		copier, err := NewCopier(Mount{Root: dstRoot})
		require.NoError(b, err)
		require.NoError(b, copier.Copy(ctx, Mount{Root: srcRoot}, "/", "/", opts))
		require.NoError(b, copier.Close())
	}
}
