//go:build linux

package layercopy

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/continuity/sysx"
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

func TestCopyDirectoryFollowsOverlayDestSymlinkDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	viewRoot := filepath.Join(root, "view")
	upperRoot := filepath.Join(root, "upper")
	require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "usr", "lib64"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "usr", "lib64", "libfoo.so"), []byte("foo"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(viewRoot, "usr", "lib"), 0o755))
	require.NoError(t, os.Symlink("lib", filepath.Join(viewRoot, "usr", "lib64")))
	require.NoError(t, os.Mkdir(upperRoot, 0o755))

	copier, err := NewCopier(Mount{
		Root: viewRoot,
		Mount: &mount.Mount{
			Type:    "overlay",
			Options: []string{"upperdir=" + upperRoot},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.Copy(context.Background(), Mount{Root: srcRoot}, "/", "/", CopyOptions{
		CopyDirContents: true,
		ReplaceExisting: true,
	})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(upperRoot, "usr", "lib", "libfoo.so"))
	require.NoError(t, err)
	require.Equal(t, "foo", string(got))
	_, err = os.Lstat(filepath.Join(upperRoot, "usr", "lib64"))
	require.True(t, os.IsNotExist(err))
	link, err := os.Readlink(filepath.Join(viewRoot, "usr", "lib64"))
	require.NoError(t, err)
	require.Equal(t, "lib", link)
}

func TestMkdirReplaceExistingOverlayLowerFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	viewRoot := filepath.Join(root, "view")
	upperRoot := filepath.Join(root, "upper")
	require.NoError(t, os.Mkdir(viewRoot, 0o755))
	require.NoError(t, os.Mkdir(upperRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(viewRoot, "node"), []byte("old"), 0o644))

	copier, err := NewCopier(Mount{
		Root: viewRoot,
		Mount: &mount.Mount{
			Type:    "overlay",
			Options: []string{"upperdir=" + upperRoot, "userxattr"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.Mkdir(context.Background(), "/node", CopyOptions{
		ReplaceExisting: true,
	})
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(upperRoot, "node"))
	require.NoError(t, err)
	require.True(t, info.IsDir())
	requireOpaqueDir(t, filepath.Join(upperRoot, "node"))
}

func TestMkdirReplaceExistingHiddenUpperPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	viewRoot := filepath.Join(root, "view")
	upperRoot := filepath.Join(root, "upper")
	require.NoError(t, os.Mkdir(viewRoot, 0o755))
	require.NoError(t, os.Mkdir(upperRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(upperRoot, "node"), []byte("hidden"), 0o644))

	copier, err := NewCopier(Mount{
		Root: viewRoot,
		Mount: &mount.Mount{
			Type:    "overlay",
			Options: []string{"upperdir=" + upperRoot, "userxattr"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.Mkdir(context.Background(), "/node", CopyOptions{
		ReplaceExisting: true,
	})
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(upperRoot, "node"))
	require.NoError(t, err)
	require.True(t, info.IsDir())
	requireOpaqueDir(t, filepath.Join(upperRoot, "node"))
}

func TestRemoveForReplaceDirectoryOverOverlayLowerFileMarksOpaque(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	viewRoot := filepath.Join(root, "view")
	upperRoot := filepath.Join(root, "upper")
	require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "node"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "node", "new.txt"), []byte("new"), 0o644))
	require.NoError(t, os.Mkdir(viewRoot, 0o755))
	require.NoError(t, os.Mkdir(upperRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(viewRoot, "node"), []byte("old"), 0o644))

	dst, err := newDestination(Mount{
		Root: viewRoot,
		Mount: &mount.Mount{
			Type:    "overlay",
			Options: []string{"upperdir=" + upperRoot, "userxattr"},
		},
	})
	require.NoError(t, err)

	srcInfo, err := os.Stat(filepath.Join(srcRoot, "node"))
	require.NoError(t, err)

	err = dst.removeForReplace("/node", srcInfo, CopyOptions{
		ReplaceExisting: true,
	})
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(upperRoot, "node"))
	require.NoError(t, err)
	require.True(t, info.IsDir())
	requireOpaqueDir(t, filepath.Join(upperRoot, "node"))
}

func TestCopyDirectoryReplaceExistingOverlayLowerFileMarksOpaque(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	viewRoot := filepath.Join(root, "view")
	upperRoot := filepath.Join(root, "upper")
	require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "node"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "node", "new.txt"), []byte("new"), 0o644))
	require.NoError(t, os.Mkdir(viewRoot, 0o755))
	require.NoError(t, os.Mkdir(upperRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(viewRoot, "node"), []byte("old"), 0o644))

	copier, err := NewCopier(Mount{
		Root: viewRoot,
		Mount: &mount.Mount{
			Type:    "overlay",
			Options: []string{"upperdir=" + upperRoot, "userxattr"},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.Copy(context.Background(), Mount{Root: srcRoot}, "/node", "/node", CopyOptions{
		ReplaceExisting: true,
	})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(upperRoot, "node", "new.txt"))
	require.NoError(t, err)
	require.Equal(t, "new", string(got))
	requireOpaqueDir(t, filepath.Join(upperRoot, "node"))
}

func requireOpaqueDir(t *testing.T, path string) {
	t.Helper()

	val, err := sysx.LGetxattr(path, "user.overlay.opaque")
	require.NoError(t, err)
	require.Equal(t, []byte{'y'}, val)
}
