//go:build linux

package layercopy

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCopyFileHardlinksFromSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	dstRoot := filepath.Join(root, "dst")
	require.NoError(t, os.Mkdir(srcRoot, 0o755))
	require.NoError(t, os.Mkdir(dstRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "file.txt"), []byte("hello"), 0o644))

	copier, err := NewCopier(Mount{Root: dstRoot})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.CopyFile(context.Background(), Mount{Root: srcRoot}, "/file.txt", "/copied.txt", CopyOptions{
		ReplaceExisting: true,
	})
	require.NoError(t, err)

	srcInfo, err := os.Stat(filepath.Join(srcRoot, "file.txt"))
	require.NoError(t, err)
	dstInfo, err := os.Stat(filepath.Join(dstRoot, "copied.txt"))
	require.NoError(t, err)
	require.Equal(t, statInode(srcInfo.Sys().(*syscall.Stat_t)), statInode(dstInfo.Sys().(*syscall.Stat_t)))
}

func TestCopyFileDisableHardlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	dstRoot := filepath.Join(root, "dst")
	require.NoError(t, os.Mkdir(srcRoot, 0o755))
	require.NoError(t, os.Mkdir(dstRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "file.txt"), []byte("hello"), 0o644))

	copier, err := NewCopier(Mount{Root: dstRoot})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.CopyFile(context.Background(), Mount{Root: srcRoot}, "/file.txt", "/copied.txt", CopyOptions{
		ReplaceExisting:  true,
		DisableHardlinks: true,
	})
	require.NoError(t, err)

	srcInfo, err := os.Stat(filepath.Join(srcRoot, "file.txt"))
	require.NoError(t, err)
	dstInfo, err := os.Stat(filepath.Join(dstRoot, "copied.txt"))
	require.NoError(t, err)
	require.NotEqual(t, statInode(srcInfo.Sys().(*syscall.Stat_t)), statInode(dstInfo.Sys().(*syscall.Stat_t)))

	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "file.txt"), []byte("mutated"), 0o644))
	got, err := os.ReadFile(filepath.Join(dstRoot, "copied.txt"))
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))
}

func TestCopyDirectoryDisableSourceHardlinksPreservesInternalHardlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	dstRoot := filepath.Join(root, "dst")
	require.NoError(t, os.Mkdir(srcRoot, 0o755))
	require.NoError(t, os.Mkdir(dstRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "file.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.Link(filepath.Join(srcRoot, "file.txt"), filepath.Join(srcRoot, "linked.txt")))

	copier, err := NewCopier(Mount{Root: dstRoot})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.Copy(context.Background(), Mount{Root: srcRoot}, "/", "/", CopyOptions{
		CopyDirContents:        true,
		ReplaceExisting:        true,
		DisableSourceHardlinks: true,
	})
	require.NoError(t, err)

	srcInfo, err := os.Stat(filepath.Join(srcRoot, "file.txt"))
	require.NoError(t, err)
	dstInfo, err := os.Stat(filepath.Join(dstRoot, "file.txt"))
	require.NoError(t, err)
	dstLinkInfo, err := os.Stat(filepath.Join(dstRoot, "linked.txt"))
	require.NoError(t, err)

	srcInode := statInode(srcInfo.Sys().(*syscall.Stat_t))
	dstInode := statInode(dstInfo.Sys().(*syscall.Stat_t))
	dstLinkInode := statInode(dstLinkInfo.Sys().(*syscall.Stat_t))
	require.NotEqual(t, srcInode, dstInode)
	require.Equal(t, dstInode, dstLinkInode)

	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "file.txt"), []byte("mutated"), 0o644))
	got, err := os.ReadFile(filepath.Join(dstRoot, "file.txt"))
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))
}

func TestCopyFileDestPathHintIsDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	dstRoot := filepath.Join(root, "dst")
	require.NoError(t, os.Mkdir(srcRoot, 0o755))
	require.NoError(t, os.Mkdir(dstRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "archive.tar"), []byte("not really a tar"), 0o644))

	copier, err := NewCopier(Mount{Root: dstRoot})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.CopyFile(context.Background(), Mount{Root: srcRoot}, "/archive.tar", "/out", CopyOptions{
		ReplaceExisting:   true,
		DestPathHintIsDir: true,
	})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dstRoot, "out", "archive.tar"))
	require.NoError(t, err)
	require.Equal(t, "not really a tar", string(got))
}

func TestCopyDirectoryCreatesFilteredParents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	dstRoot := filepath.Join(root, "dst")
	require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "a", "b"), 0o755))
	require.NoError(t, os.Mkdir(dstRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "a", "b", "keep.txt"), []byte("keep"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "a", "skip.txt"), []byte("skip"), 0o644))

	copier, err := NewCopier(Mount{Root: dstRoot})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.Copy(context.Background(), Mount{Root: srcRoot}, "/", "/out", CopyOptions{
		Filter: Filter{
			Include: []string{"a/b/keep.txt"},
		},
		CopyDirContents: true,
		ReplaceExisting: true,
	})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dstRoot, "out", "a", "b", "keep.txt"))
	require.NoError(t, err)
	require.Equal(t, "keep", string(got))
	_, err = os.Stat(filepath.Join(dstRoot, "out", "a", "skip.txt"))
	require.True(t, os.IsNotExist(err))
}

func TestCopyDirectoryOnlyCopiesSparsePaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	dstRoot := filepath.Join(root, "dst")
	require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "a", "b"), 0o755))
	require.NoError(t, os.Mkdir(dstRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "a", "b", "keep.txt"), []byte("keep"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "a", "skip.txt"), []byte("skip"), 0o644))

	copier, err := NewCopier(Mount{Root: dstRoot})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.Copy(context.Background(), Mount{Root: srcRoot}, "/", "/out", CopyOptions{
		Filter: Filter{
			Only: map[string]struct{}{
				"a/b/keep.txt":  {},
				"a/deleted.txt": {},
			},
		},
		CopyDirContents: true,
		ReplaceExisting: true,
	})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dstRoot, "out", "a", "b", "keep.txt"))
	require.NoError(t, err)
	require.Equal(t, "keep", string(got))
	_, err = os.Stat(filepath.Join(dstRoot, "out", "a", "skip.txt"))
	require.True(t, os.IsNotExist(err))
}
