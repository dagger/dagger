package client

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagger/dagger/util/patternmatcher"
	"github.com/stretchr/testify/require"
)

// Keep the reference deliberately unpruned: pruning must only remove work,
// including for patterns whose wildcards span directories.
func globHostPathFullWalk(ctx context.Context, root, pattern string) ([]string, error) {
	pat, err := patternmatcher.NewPattern(pattern)
	if err != nil {
		return nil, err
	}
	matches := []string{}
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil || rel == "." {
			return err
		}
		match, err := pat.Match(rel)
		if err != nil {
			return err
		}
		if match {
			rel = filepath.ToSlash(rel)
			if entry.IsDir() {
				rel += "/"
			}
			matches = append(matches, rel)
		}
		return nil
	})
	return matches, err
}

func TestGlobHostPathLiteralDirectoryPrefix(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"a.go", "a/a_test.go", "a/b/keep.go", "a/b/c/deep.go",
		"a/bb.go", "a/bb/sibling_test.go", "a/beta/pkg_test.go",
		"a/α/π_test.go", "a/[bracket]/literal.go", "a-extra/pkg_test.go",
		"z/deep/other_test.go",
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, nil, 0o600))
	}
	if runtime.GOOS != "windows" {
		outside := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(outside, "child"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(outside, "child", "outside.go"), nil, 0o600))
		require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	}
	patterns := []string{
		"*", "**", "a/*", "a/b*", "*_test.go", "a/*_test.go", "a/[ab][^a]*.go", "a/**/*.go", "a/*/*.go", "a/b?/*.go", "a/[bc]/*.go",
		"*/b/*.go", "**/*.go", "a/*/**", "a/*/*", "missing/*.go", "a", "a/**",
		"a-extra/*.go", "!a/*.go", "[a]*/*.go", "a/α/*.go", "link/child/*.go",
		"/a/*.go", "../a/*.go", "a/./b/*.go", "a/b/../*.go",
	}
	if runtime.GOOS != "windows" {
		patterns = append(patterns, `a/\[bracket\]/*.go`, `a/b\x2fkeep.go`, `a/b[\x2f]keep.go`)
	}
	for _, pattern := range patterns {
		t.Run(pattern, func(t *testing.T) {
			want, err := globHostPathFullWalk(t.Context(), root, pattern)
			require.NoError(t, err)
			got, err := globHostPath(t.Context(), root, pattern)
			require.NoError(t, err)
			require.Equal(t, want, got, "glob results and traversal order must be preserved")
		})
	}
}

func TestGlobHostPathMissingPrefixStillChecksCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := globHostPath(ctx, t.TempDir(), "missing/*.go")
	require.ErrorIs(t, err, context.Canceled)
}

func BenchmarkGlobHostPathLiteralDirectoryPrefix(b *testing.B) {
	root := b.TempDir()
	for branch := range 24 {
		for pkg := range 24 {
			dir := filepath.Join(root, fmt.Sprintf("module%02d", branch), fmt.Sprintf("pkg%02d", pkg))
			require.NoError(b, os.MkdirAll(dir, 0o755))
			for file := range 8 {
				require.NoError(b, os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%02d_test.go", file)), nil, 0o600))
			}
		}
	}
	for _, pattern := range []string{"module00/pkg00/*_test.go", "module00/**/*.go", "**/*.go"} {
		b.Run(pattern, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				matches, err := globHostPath(b.Context(), root, pattern)
				require.NoError(b, err)
				require.NotEmpty(b, matches)
			}
		})
	}
}
