//go:build linux

package layercopy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

func TestCopyOverlaySourceStopsAtNonDirectoryLayer(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	lowerRoot := filepath.Join(root, "lower")
	middleRoot := filepath.Join(root, "middle")
	upperRoot := filepath.Join(root, "upper")
	viewRoot := filepath.Join(root, "view")
	dstRoot := filepath.Join(root, "dst")

	for _, dir := range []string{
		filepath.Join(lowerRoot, "d", "sub"),
		middleRoot,
		filepath.Join(upperRoot, "d"),
		filepath.Join(viewRoot, "d"),
		dstRoot,
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(lowerRoot, "d", "old.txt"), []byte("old"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(lowerRoot, "d", "sub", "deep.txt"), []byte("old"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(middleRoot, "d"), []byte("cover"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(upperRoot, "d", "new.txt"), []byte("new"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(viewRoot, "d", "new.txt"), []byte("new"), 0o644))

	copier, err := NewCopier(Mount{Root: dstRoot})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.Copy(context.Background(), Mount{
		Root: viewRoot,
		Mount: &mount.Mount{
			Type: "overlay",
			Options: []string{
				"lowerdir=" + strings.Join([]string{upperRoot, middleRoot, lowerRoot}, ":"),
			},
		},
	}, "/", "/", CopyOptions{
		CopyDirContents: true,
		ReplaceExisting: true,
	})
	require.NoError(t, err)

	contents, err := os.ReadFile(filepath.Join(dstRoot, "d", "new.txt"))
	require.NoError(t, err)
	require.Equal(t, "new", string(contents))
	require.NoFileExists(t, filepath.Join(dstRoot, "d", "old.txt"))
	require.NoDirExists(t, filepath.Join(dstRoot, "d", "sub"))
}

func TestCopyOverlaySourceDoesNotFollowSymlinkLayer(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	lowerRoot := filepath.Join(root, "lower")
	upperRoot := filepath.Join(root, "upper")
	viewRoot := filepath.Join(root, "view")
	dstRoot := filepath.Join(root, "dst")

	for _, dir := range []string{
		filepath.Join(lowerRoot, "target"),
		filepath.Join(upperRoot, "d"),
		filepath.Join(viewRoot, "d"),
		dstRoot,
	} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(lowerRoot, "target", "hidden.txt"), []byte("hidden"), 0o644))
	require.NoError(t, os.Symlink("target", filepath.Join(lowerRoot, "d")))
	require.NoError(t, os.WriteFile(filepath.Join(upperRoot, "d", "visible.txt"), []byte("visible"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(viewRoot, "d", "visible.txt"), []byte("visible"), 0o644))

	copier, err := NewCopier(Mount{Root: dstRoot})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	err = copier.Copy(context.Background(), Mount{
		Root: viewRoot,
		Mount: &mount.Mount{
			Type: "overlay",
			Options: []string{
				"lowerdir=" + strings.Join([]string{upperRoot, lowerRoot}, ":"),
			},
		},
	}, "/", "/", CopyOptions{
		CopyDirContents: true,
		ReplaceExisting: true,
	})
	require.NoError(t, err)

	contents, err := os.ReadFile(filepath.Join(dstRoot, "d", "visible.txt"))
	require.NoError(t, err)
	require.Equal(t, "visible", string(contents))
	require.NoFileExists(t, filepath.Join(dstRoot, "d", "hidden.txt"))
}

func TestCopierScopesOverlaySourceCacheByMount(t *testing.T) {
	t.Parallel()

	copier := &Copier{sourceCaches: map[sourceCacheKey]*sourceCache{}}
	firstMount := &mount.Mount{Type: "overlay", Options: []string{"lowerdir=/first"}}
	secondMount := &mount.Mount{Type: "overlay", Options: []string{"lowerdir=/first"}}

	first, err := copier.sourceForCopy(Mount{Root: "/view", Mount: firstMount})
	require.NoError(t, err)
	repeated, err := copier.sourceForCopy(Mount{Root: "/view", Mount: firstMount})
	require.NoError(t, err)
	differentRoot, err := copier.sourceForCopy(Mount{Root: "/other", Mount: firstMount})
	require.NoError(t, err)
	differentMount, err := copier.sourceForCopy(Mount{Root: "/view", Mount: secondMount})
	require.NoError(t, err)

	require.Same(t, first.cache, repeated.cache)
	require.NotSame(t, first.cache, differentRoot.cache)
	require.NotSame(t, first.cache, differentMount.cache)
	require.Len(t, copier.sourceCaches, 3)
}

func TestOverlayAncestorMinLayerCachesCumulativeBounds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	layers := []string{
		filepath.Join(root, "lower"),
		filepath.Join(root, "middle"),
		filepath.Join(root, "upper"),
	}
	require.NoError(t, os.MkdirAll(filepath.Join(layers[0], "a", "b"), 0o755))
	require.NoError(t, os.MkdirAll(layers[1], 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(layers[1], "a"), []byte("cover"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(layers[2], "a", "b"), 0o755))

	cache := &sourceCache{ancestorMinLayers: map[string]int{}}
	src := &source{overlay: true, layers: layers, cache: cache}
	minLayer, err := src.overlayAncestorMinLayer("a/b/file.txt")
	require.NoError(t, err)
	require.Equal(t, 1, minLayer)
	require.Equal(t, map[string]int{
		"":    0,
		"a":   1,
		"a/b": 1,
	}, cache.ancestorMinLayers)

	minLayer, err = src.overlayAncestorMinLayer("a/b/other.txt")
	require.NoError(t, err)
	require.Equal(t, 1, minLayer)
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

func TestCopyEntryMissingSourceAfterFilter(t *testing.T) {
	t.Parallel()

	missingErr := &os.PathError{Op: "lstat", Path: "/src/gone.txt", Err: os.ErrNotExist}

	t.Run("excluded", func(t *testing.T) {
		matcher, err := newMatcher("/src", Filter{
			Only: map[string]struct{}{
				"keep.txt": {},
			},
		})
		require.NoError(t, err)

		err = (&Copier{}).copyEntry(context.Background(), &source{}, matcher, sourceEntry{
			Rel:      "gone.txt",
			ViewPath: "/src/gone.txt",
			RealPath: "/src/gone.txt",
			StatErr:  missingErr,
		}, "/dst/gone.txt", CopyOptions{}, matchState{}, nil, resolvedParent{})
		require.NoError(t, err)
	})

	t.Run("included", func(t *testing.T) {
		matcher, err := newMatcher("/src", Filter{
			Only: map[string]struct{}{
				"gone.txt": {},
			},
		})
		require.NoError(t, err)

		err = (&Copier{}).copyEntry(context.Background(), &source{}, matcher, sourceEntry{
			Rel:      "gone.txt",
			ViewPath: "/src/gone.txt",
			RealPath: "/src/gone.txt",
			StatErr:  missingErr,
		}, "/dst/gone.txt", CopyOptions{}, matchState{}, nil, resolvedParent{})
		require.ErrorIs(t, err, os.ErrNotExist)
	})
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

	realPath, err := dst.removeForReplace("/node", resolvedParent{}, srcInfo, CopyOptions{
		ReplaceExisting: true,
	})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(upperRoot, "node"), realPath)

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

// TestMaterializeDeepOverlayAncestors covers materializing several levels of
// pre-existing view directories into an empty upper in one step. Ancestors are
// resolved once and then created parent-first; creating them in any other
// order fails because the parent does not exist yet.
func TestMaterializeDeepOverlayAncestors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	viewRoot := filepath.Join(root, "view")
	upperRoot := filepath.Join(root, "upper")
	require.NoError(t, os.Mkdir(srcRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "leaf.txt"), []byte("leaf"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(viewRoot, "a", "b", "c", "d"), 0o755))
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

	err = copier.Copy(context.Background(), Mount{Root: srcRoot}, "/", "/a/b/c/d", CopyOptions{
		CopyDirContents: true,
		ReplaceExisting: true,
	})
	require.NoError(t, err)

	for _, dir := range []string{"a", "a/b", "a/b/c", "a/b/c/d"} {
		info, err := os.Lstat(filepath.Join(upperRoot, dir))
		require.NoErrorf(t, err, "ancestor %q was not materialized", dir)
		require.True(t, info.IsDir())
	}
	got, err := os.ReadFile(filepath.Join(upperRoot, "a", "b", "c", "d", "leaf.txt"))
	require.NoError(t, err)
	require.Equal(t, "leaf", string(got))
}

func TestForgetSourceLinksBetweenCopies(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "A"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "B"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "A", "a"), []byte("A"), 0o644))
	require.NoError(t, os.Link(filepath.Join(srcRoot, "A", "a"), filepath.Join(srcRoot, "A", "b")))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "B", "a"), []byte("B"), 0o644))

	mode := os.FileMode(0o640)
	for _, tc := range []struct {
		name   string
		copies [][2]string
		opts   CopyOptions
		want   map[string]string
	}{
		{
			// The second copy replaces the destination the first linked.
			name:   "same file twice",
			copies: [][2]string{{"/A/a", "/a"}, {"/A/a", "/a"}},
			opts:   CopyOptions{ReplaceExisting: true},
			want:   map[string]string{"a": "A"},
		},
		{
			// A/b is A/a's inode, but /a holds B/a's content by then.
			name:   "replaced link",
			copies: [][2]string{{"/A/a", "/a"}, {"/B/a", "/a"}, {"/A/b", "/b"}},
			opts:   CopyOptions{ReplaceExisting: true, Mode: &mode},
			want:   map[string]string{"a": "B", "b": "A"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dstRoot := filepath.Join(root, strings.ReplaceAll(tc.name, " ", "-"))
			require.NoError(t, os.Mkdir(dstRoot, 0o755))
			copier, err := NewCopier(Mount{Root: dstRoot})
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, copier.Close())
			})
			for _, cp := range tc.copies {
				copier.ForgetSourceLinks()
				require.NoError(t, copier.CopyFile(context.Background(), Mount{Root: srcRoot}, cp[0], cp[1], tc.opts))
			}
			for name, want := range tc.want {
				got, err := os.ReadFile(filepath.Join(dstRoot, name))
				require.NoError(t, err)
				require.Equal(t, want, string(got))
			}
		})
	}
}

// filterTestTree is a source tree for the include-filter tests. Directories
// end in a slash.
var filterTestTree = []string{
	"a/",
	"a/b/",
	"a/b/keep.txt",
	"a/b/other.go",
	"a/b/c/",
	"a/b/c/deep.txt",
	"a/skip.txt",
	"a/sibling/",
	"a/sibling/x/",
	"a/sibling/x/y.txt",
	"ab/",
	"ab/z.txt",
	"top.txt",
	"other/",
	"other/b/",
	"other/b/keep.txt",
}

func writeFilterTestTree(t *testing.T, root string) {
	t.Helper()
	for _, p := range filterTestTree {
		path := filepath.Join(root, p)
		if strings.HasSuffix(p, "/") {
			require.NoError(t, os.MkdirAll(path, 0o755))
			continue
		}
		require.NoError(t, os.WriteFile(path, []byte(p), 0o644))
	}
}

// filteredCopy copies srcRoot into a new destination with the given filter.
// It returns the copied paths, directories ending in a slash, and the source
// directories the copy read. If noPrune is set, the copy walks the whole tree,
// as it did before include filters pruned directories.
func filteredCopy(t *testing.T, srcRoot string, filter Filter, noPrune bool) (copied []string, read []string) {
	t.Helper()

	dstRoot := t.TempDir()
	copier, err := NewCopier(Mount{Root: dstRoot})
	require.NoError(t, err)

	src, err := copier.sourceForCopy(Mount{Root: srcRoot})
	require.NoError(t, err)
	src.onReadDir = func(rel string) {
		read = append(read, rel)
	}
	m, err := newMatcher(srcRoot, filter)
	require.NoError(t, err)
	if noPrune {
		m.onlyPrefixIncludes = false
	}
	require.NoError(t, copier.copy(context.Background(), src, m, "/", "/out", CopyOptions{
		Filter:          filter,
		CopyDirContents: true,
		ReplaceExisting: true,
	}))
	require.NoError(t, copier.Close())

	outRoot := filepath.Join(dstRoot, "out")
	require.NoError(t, filepath.WalkDir(outRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(outRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			rel += "/"
		}
		copied = append(copied, rel)
		return nil
	}))
	return copied, read
}

func TestCopyDirectoryIncludeSkipsNonMatchingDirs(t *testing.T) {
	t.Parallel()

	srcRoot := t.TempDir()
	writeFilterTestTree(t, srcRoot)

	copied, read := filteredCopy(t, srcRoot, Filter{
		Include: []string{"a/b/keep.txt"},
	}, false)
	require.Equal(t, []string{"a/", "a/b/", "a/b/keep.txt"}, copied)
	// Only the root and the directories on the way to the include pattern are
	// read. The non-matching siblings a/b/c, a/sibling, ab and other are not.
	require.ElementsMatch(t, []string{"", "a", "a/b"}, read)

	copied, read = filteredCopy(t, srcRoot, Filter{
		Include: []string{"a/b/*"},
	}, false)
	require.Equal(t, []string{"a/", "a/b/", "a/b/c/", "a/b/c/deep.txt", "a/b/keep.txt", "a/b/other.go"}, copied)
	require.ElementsMatch(t, []string{"", "a", "a/b", "a/b/c"}, read)
}

func TestCopyDirectoryIncludePruningKeepsCopiedPaths(t *testing.T) {
	t.Parallel()

	srcRoot := t.TempDir()
	writeFilterTestTree(t, srcRoot)

	for _, tc := range []struct {
		name   string
		filter Filter
		// prunes is whether the copy walks fewer directories than the full tree.
		prunes bool
	}{
		{name: "literal file", filter: Filter{Include: []string{"a/b/keep.txt"}}, prunes: true},
		{name: "literal dir", filter: Filter{Include: []string{"a/b"}}, prunes: true},
		{name: "trailing star", filter: Filter{Include: []string{"a/b/*"}}, prunes: true},
		{name: "trailing double star", filter: Filter{Include: []string{"a/**"}}, prunes: true},
		{name: "dir name prefix of sibling", filter: Filter{Include: []string{"a"}}, prunes: true},
		{name: "several literals", filter: Filter{Include: []string{"top.txt", "other/b"}}, prunes: true},
		{name: "literal with exclude", filter: Filter{Include: []string{"a"}, Exclude: []string{"a/b/c"}}, prunes: true},
		{name: "literal with negation", filter: Filter{Include: []string{"a", "!a/b"}}, prunes: true},
		{name: "negation of a sibling", filter: Filter{Include: []string{"a/b/keep.txt", "!other"}}, prunes: true},
		{name: "leading dot slash", filter: Filter{Include: []string{"./a/b/keep.txt"}}, prunes: true},
		{name: "leading slash", filter: Filter{Include: []string{"/a/b/keep.txt"}}, prunes: true},
		{name: "wildcard in dir", filter: Filter{Include: []string{"*/b/keep.txt"}}},
		{name: "wildcard in name", filter: Filter{Include: []string{"a/b/*.go"}}},
		{name: "question mark", filter: Filter{Include: []string{"a/?/keep.txt"}}},
		{name: "character class", filter: Filter{Include: []string{"[ao]*/b"}}},
		{name: "wildcard dir then trailing double star", filter: Filter{Include: []string{"a/*/**"}}},
		{name: "trailing double star then star", filter: Filter{Include: []string{"a/**/*"}}},
		{name: "double star in middle", filter: Filter{Include: []string{"a/**/deep.txt"}}},
		{name: "leading double star", filter: Filter{Include: []string{"**/keep.txt"}}},
		{name: "double star only", filter: Filter{Include: []string{"**"}}},
		{name: "wildcard mixed with literal", filter: Filter{Include: []string{"top.txt", "*/x"}}},
		{name: "wildcard with negation", filter: Filter{Include: []string{"**/*.txt", "!a/sibling"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			want, fullRead := filteredCopy(t, srcRoot, tc.filter, true)
			got, read := filteredCopy(t, srcRoot, tc.filter, false)
			require.Equal(t, want, got)
			if tc.prunes {
				require.Less(t, len(read), len(fullRead))
			} else {
				require.ElementsMatch(t, fullRead, read)
			}
		})
	}
}
