package fsutil

import (
	"context"
	"errors"
	"fmt"
	gofs "io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagger/dagger/internal/fsutil/types"
	"github.com/stretchr/testify/require"
)

// Embedding only FS hides the optional native walker, retaining the original
// full walk as the reference without maintaining another filter implementation.
type ordinaryWalkFS struct{ FS }

func readLiteralInclude(t *testing.T, root string, opt *FilterOpt, ordinary bool, target, stop string, cancelAfter string) ([]*types.Stat, error) {
	t.Helper()
	base, err := NewFS(root)
	require.NoError(t, err)
	if ordinary {
		base = ordinaryWalkFS{base}
	}
	filtered, err := NewFilterFS(base, opt)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if cancelAfter == "before" {
		cancel()
	}
	var stats []*types.Stat
	err = filtered.Walk(ctx, target, func(path string, entry gofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stats = append(stats, info.Sys().(*types.Stat))
		if path == cancelAfter {
			cancel()
		}
		if path == stop {
			return filepath.SkipDir
		}
		return nil
	})
	return stats, err
}

func TestFilterFSLiteralIncludeParity(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/keep/file.txt", "a/keep/deep/data", "a/other/file.txt", "a/α/π.go", "a/[bracket]/file", "a/plain", "spare/file"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(name), 0o640))
	}
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Link(filepath.Join(root, "a/keep/file.txt"), filepath.Join(root, "a/keep/hardlink")))
		require.NoError(t, os.Symlink("a", filepath.Join(root, "link")))
		require.NoError(t, os.Symlink("keep", filepath.Join(root, "a/link")))
	}
	for _, test := range []struct {
		include []string
		exclude []string
	}{
		{include: []string{"a/keep"}},
		{include: []string{"a/keep"}, exclude: []string{"a/keep/*"}},
		{include: []string{"a/keep/file.txt"}},
		{include: []string{"a"}, exclude: []string{"a/keep"}},
		{include: []string{"a"}, exclude: []string{"**", "!a/keep/file.txt"}},
		{include: []string{"a/keep"}, exclude: []string{"**", "!a/keep/**"}},
		{include: []string{"link/keep"}},
		{include: []string{"a/link"}},
		{include: []string{"a/link/file.txt"}},
		{include: []string{"a/keep/missing"}},
		{include: []string{"missing/deep/file"}},
		{include: []string{"a/plain/child"}},
		{include: []string{"a/α"}},
		{include: []string{`a/\[bracket\]`}},
		{include: []string{"a/keep", "a/other"}},
		{include: []string{"a/*"}},
		{include: []string{"!a"}},
		{include: []string{"/a"}},
		{include: []string{"../a"}},
		{include: []string{"."}},
	} {
		t.Run(fmt.Sprint(test.include, test.exclude), func(t *testing.T) {
			for _, target := range []string{"", ".", string(filepath.Separator)} {
				opt := &FilterOpt{IncludePatterns: test.include, ExcludePatterns: test.exclude}
				want, wantErr := readLiteralInclude(t, root, opt, true, target, "", "")
				got, gotErr := readLiteralInclude(t, root, opt, false, target, "", "")
				require.Equal(t, wantErr, gotErr)
				require.Equal(t, want, got, "complete stats and order differ for target %q", target)
			}
		})
	}
	for _, test := range []struct{ stop, cancel string }{{stop: "a"}, {stop: "a/keep"}, {cancel: "before"}, {cancel: "a"}, {cancel: "a/keep"}} {
		opt := &FilterOpt{IncludePatterns: []string{"a/keep/file.txt"}}
		want, wantErr := readLiteralInclude(t, root, opt, true, "", test.stop, test.cancel)
		got, gotErr := readLiteralInclude(t, root, opt, false, "", test.stop, test.cancel)
		require.Equal(t, errors.Is(wantErr, context.Canceled), errors.Is(gotErr, context.Canceled), "stop %q cancel %q: want %v got %v", test.stop, test.cancel, wantErr, gotErr)
		require.Equal(t, want, got)
	}
	for _, result := range []MapResult{MapResultKeep, MapResultExclude, MapResultSkipDir} {
		opt := &FilterOpt{IncludePatterns: []string{"a/keep/file.txt"}, Map: func(path string, stat *types.Stat) MapResult {
			stat.Uid = 123
			stat.Gid = 456
			if path == "a" {
				return result
			}
			return MapResultKeep
		}}
		want, wantErr := readLiteralInclude(t, root, opt, true, "", "", "")
		got, gotErr := readLiteralInclude(t, root, opt, false, "", "", "")
		require.Equal(t, wantErr, gotErr)
		require.Equal(t, want, got)
	}
}

func TestFilterFSLiteralIncludeUnreadableAncestor(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions without root bypass")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "closed")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), nil, 0o600))
	require.NoError(t, os.Chmod(dir, 0o100))
	t.Cleanup(func() { require.NoError(t, os.Chmod(dir, 0o700)) })
	opt := &FilterOpt{IncludePatterns: []string{"closed/file"}}
	want, wantErr := readLiteralInclude(t, root, opt, true, "", "", "")
	got, gotErr := readLiteralInclude(t, root, opt, false, "", "", "")
	require.Equal(t, wantErr, gotErr)
	require.Equal(t, want, got)
}

func BenchmarkFilterFSLiteralInclude(b *testing.B) {
	for _, width := range []int{8, 128, 2048} {
		root := b.TempDir()
		require.NoError(b, os.MkdirAll(filepath.Join(root, "a/b/selected"), 0o750))
		require.NoError(b, os.WriteFile(filepath.Join(root, "a/b/selected/child"), nil, 0o600))
		for i := range width {
			require.NoError(b, os.Mkdir(filepath.Join(root, fmt.Sprintf("sibling%04d", i)), 0o750))
		}
		b.Run(fmt.Sprint(width), func(b *testing.B) {
			base, err := NewFS(root)
			require.NoError(b, err)
			for b.Loop() {
				filtered, err := NewFilterFS(base, &FilterOpt{IncludePatterns: []string{"a/b/selected"}, ExcludePatterns: []string{"a/b/selected/*"}})
				require.NoError(b, err)
				count := 0
				err = filtered.Walk(b.Context(), "", func(_ string, _ gofs.DirEntry, err error) error { count++; return err })
				require.NoError(b, err)
				require.Equal(b, 3, count)
			}
		})
	}
}
