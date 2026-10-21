package client

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/util/patternmatcher"
	"github.com/stretchr/testify/require"
)

func TestGlobHostPathLiteralPrefix(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"sdk/root.go", "sdk/nested/deep.go", "sdk/nested/note.txt", "sdk-other/other.go", "elsewhere/deep/other.go", "unicode/été/file.go", "literal[dir]/file.go"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, nil, 0o600))
	}
	for _, pattern := range []string{"sdk/**/*.go", "sdk/nested/*.go", "sdk/*/*.go", "sdk/[nr]*/*.go", "sdk/??sted/*.go", "sdk*/**/*.go", "**/*.go", "sdk/**", "sdk/*", "sdk/nested", "missing/**/*.go", "!sdk/**/*.go", "unicode/été/**/*.go"} {
		t.Run(pattern, func(t *testing.T) {
			// An unrestricted walk is the oracle: pruning must preserve its order
			// and every file or directory accepted by the existing matcher.
			pat, err := patternmatcher.NewPattern(pattern)
			require.NoError(t, err)
			want := []string{}
			require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(root, path)
				if err != nil || rel == "." {
					return err
				}
				match, err := pat.Match(rel)
				if err != nil {
					return err
				}
				if match {
					name := filepath.ToSlash(rel)
					if d.IsDir() {
						name += "/"
					}
					want = append(want, name)
				}
				return nil
			}))
			got, err := globHostPath(t.Context(), root, pattern)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func BenchmarkGlobHostPathLiteralPrefix(b *testing.B) {
	root := b.TempDir()
	for dir := range 64 {
		for file := range 8 {
			path := filepath.Join(root, fmt.Sprintf("package%02d", dir), fmt.Sprintf("file%d.go", file))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	for _, pattern := range []string{"package00/**/*.go", "**/*.go"} {
		b.Run(pattern, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				got, err := globHostPath(context.Background(), root, pattern)
				if err != nil {
					b.Fatal(err)
				}
				want := 8
				if pattern == "**/*.go" {
					want = 512
				}
				if len(got) != want {
					b.Fatalf("got %d matches, want %d", len(got), want)
				}
			}
		})
	}
}
