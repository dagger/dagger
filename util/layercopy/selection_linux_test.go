//go:build linux

package layercopy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/continuity/sysx"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func copySelection(t *testing.T, root, name string) string {
	t.Helper()
	dst := t.TempDir()
	copier, err := NewCopier(Mount{Root: dst})
	require.NoError(t, err)
	defer copier.Close()
	require.NoError(t, copier.CopySelection(context.Background(), root, name))
	return dst
}

func TestSelectionPreservesWorkingAndDanglingLinks(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src/bin"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "shared"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src/bin/python3"), []byte("python"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "shared/file"), []byte("shared"), 0644))
	links := map[string]string{
		"bin/python": "python3", "chain": "external", "external": "../shared",
		"absolute": "/shared", "dangling": "../missing", "local-dangling": "missing",
		"cycle": "cycle", "parent-cycle": "../src/parent-cycle", "self": ".",
	}
	for name, target := range links {
		require.NoError(t, os.Symlink(target, filepath.Join(root, "src", name)))
	}
	require.NoError(t, os.Symlink("file", filepath.Join(root, "shared/link")))
	dst := copySelection(t, root, "src")
	for name, target := range links {
		if name == "external" || name == "absolute" {
			continue
		}
		actual, err := os.Readlink(filepath.Join(dst, name))
		require.NoError(t, err, name)
		require.Equal(t, target, actual, name)
	}
	for _, name := range []string{"external", "absolute", "chain"} {
		data, err := os.ReadFile(filepath.Join(dst, name, "link"))
		require.NoError(t, err)
		require.Equal(t, "shared", string(data))
		target, err := os.Readlink(filepath.Join(dst, name, "link"))
		require.NoError(t, err)
		require.Equal(t, "file", target)
	}
}

func TestSelectionPreservesRootMetadata(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "src")
	require.NoError(t, os.Mkdir(selected, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(selected, "file"), []byte("x"), 0644))
	require.NoError(t, sysx.LSetxattr(selected, "user.selection", []byte("root"), 0))
	if os.Geteuid() == 0 {
		require.NoError(t, os.Chown(selected, 1234, 2345))
	}
	stamp := time.Unix(123456789, 0)
	require.NoError(t, os.Chtimes(selected, stamp, stamp))
	before, err := os.Stat(selected)
	require.NoError(t, err)
	dst := copySelection(t, root, "src")
	after, err := os.Stat(dst)
	require.NoError(t, err)
	require.Equal(t, before.Mode(), after.Mode())
	require.Equal(t, before.ModTime(), after.ModTime())
	want, got := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	require.Equal(t, want.Uid, got.Uid)
	require.Equal(t, want.Gid, got.Gid)
	require.Equal(t, want.Atim, got.Atim)
	attr, err := sysx.LGetxattr(dst, "user.selection")
	require.NoError(t, err)
	require.Equal(t, "root", string(attr))
}

func TestSelectionSharedDirectoriesStayLinear(t *testing.T) {
	root := t.TempDir()
	const levels = 20
	require.NoError(t, os.Mkdir(filepath.Join(root, "L0"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "L0/file"), []byte("x"), 0644))
	for i := 1; i <= levels; i++ {
		dir := filepath.Join(root, fmt.Sprintf("L%d", i))
		require.NoError(t, os.Mkdir(dir, 0755))
		for _, name := range []string{"a", "b"} {
			require.NoError(t, os.Symlink(fmt.Sprintf("../L%d", i-1), filepath.Join(dir, name)))
		}
	}
	dst := copySelection(t, root, fmt.Sprintf("L%d", levels))
	count := 0
	require.NoError(t, filepath.WalkDir(dst, func(_ string, _ os.DirEntry, err error) error { count++; return err }))
	require.Equal(t, 2*levels+2, count)
}

func TestSelectionPinsContentsAndMetadata(t *testing.T) {
	root, outside, dst := t.TempDir(), t.TempDir(), t.TempDir()
	file := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(file, []byte("inside"), 0640))
	require.NoError(t, sysx.LSetxattr(file, "user.selection", []byte("inside"), 0))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0600))
	parent, err := os.OpenRoot(root)
	require.NoError(t, err)
	defer parent.Close()
	f, err := parent.OpenFile("file", unix.O_PATH|unix.O_NOFOLLOW, 0)
	require.NoError(t, err)
	defer f.Close()
	ent, err := pinnedSelectionEntry(f)
	require.NoError(t, err)
	require.NoError(t, os.Rename(file, file+".saved"))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret"), file))
	copier, err := NewCopier(Mount{Root: dst})
	require.NoError(t, err)
	defer copier.Close()
	require.NoError(t, copier.copyNode(ent, "/file", resolvedParent{}, CopyOptions{DisableSourceHardlinks: true}))
	data, err := os.ReadFile(filepath.Join(dst, "file"))
	require.NoError(t, err)
	require.Equal(t, "inside", string(data))
	attr, err := sysx.LGetxattr(filepath.Join(dst, "file"), "user.selection")
	require.NoError(t, err)
	require.Equal(t, "inside", string(attr))
}

func TestSelectionPinnedDirectoryReplacement(t *testing.T) {
	root, outside, dst := t.TempDir(), t.TempDir(), t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src/file"), []byte("inside"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file"), []byte("outside"), 0644))
	f, err := os.Open(filepath.Join(root, "src"))
	require.NoError(t, err)
	defer f.Close()
	ent, err := pinnedSelectionEntry(f)
	require.NoError(t, err)
	require.NoError(t, os.Rename(filepath.Join(root, "src"), filepath.Join(root, "saved")))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "src")))
	copier, err := NewCopier(Mount{Root: dst})
	require.NoError(t, err)
	defer copier.Close()
	copy := selectionCopy{copier: copier, directories: map[inode]string{}}
	require.NoError(t, copy.directory(context.Background(), ent, "src", "/", "src"))
	data, err := os.ReadFile(filepath.Join(dst, "file"))
	require.NoError(t, err)
	require.Equal(t, "inside", string(data))
}

func BenchmarkSelectionDepth(b *testing.B) {
	for _, depth := range []int{2, 12} {
		b.Run(fmt.Sprintf("depth=%d", depth), func(b *testing.B) {
			root, dst := b.TempDir(), b.TempDir()
			dir := root
			for range depth {
				dir = filepath.Join(dir, "dir")
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				b.Fatal(err)
			}
			for i := range 2000 {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), []byte("x"), 0644); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				if err := os.RemoveAll(dst); err != nil {
					b.Fatal(err)
				}
				if err := os.Mkdir(dst, 0755); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				copier, err := NewCopier(Mount{Root: dst})
				if err != nil {
					b.Fatal(err)
				}
				if err := copier.CopySelection(context.Background(), root, "."); err != nil {
					b.Fatal(err)
				}
				if err := copier.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestSelectionResolvesDotDotAfterExternalLink(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "outside/deep"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "outside/file"), []byte("correct"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src/file"), []byte("wrong"), 0644))
	require.NoError(t, os.Symlink("../outside/deep", filepath.Join(root, "src/dir")))
	require.NoError(t, os.Symlink("dir/../file", filepath.Join(root, "src/link")))
	dst := copySelection(t, root, "src")
	content, err := os.ReadFile(filepath.Join(dst, "link"))
	require.NoError(t, err)
	require.Equal(t, "correct", string(content))
}

func TestSelectionPreservesHardlinksWithoutLinkingSource(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a"), []byte("inside"), 0644))
	require.NoError(t, os.Link(filepath.Join(root, "a"), filepath.Join(root, "b")))
	dst := copySelection(t, root, ".")
	original, err := os.Stat(filepath.Join(root, "a"))
	require.NoError(t, err)
	a, err := os.Stat(filepath.Join(dst, "a"))
	require.NoError(t, err)
	b, err := os.Stat(filepath.Join(dst, "b"))
	require.NoError(t, err)
	require.False(t, os.SameFile(original, a))
	require.True(t, os.SameFile(a, b))
}

func TestSelectionPinsSymlinkText(t *testing.T) {
	root, dst := t.TempDir(), t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("inside"), 0644))
	name := filepath.Join(root, "src/link")
	require.NoError(t, os.Symlink("../file", name))
	parent, err := os.OpenRoot(filepath.Join(root, "src"))
	require.NoError(t, err)
	defer parent.Close()
	f, err := parent.OpenFile("link", unix.O_PATH|unix.O_NOFOLLOW, 0)
	require.NoError(t, err)
	defer f.Close()
	ent, err := pinnedSelectionEntry(f)
	require.NoError(t, err)
	require.NoError(t, os.Remove(name))
	require.NoError(t, os.Symlink("/etc/hostname", name))
	src := &source{root: root}
	require.NoError(t, src.confine())
	defer src.rootHandle.Close()
	copier, err := NewCopier(Mount{Root: dst})
	require.NoError(t, err)
	defer copier.Close()
	copy := selectionCopy{copier: copier, source: src, directories: map[inode]string{}}
	require.NoError(t, copy.link(context.Background(), ent, "src/link", "/link", "src"))
	data, err := os.ReadFile(filepath.Join(dst, "link"))
	require.NoError(t, err)
	require.Equal(t, "inside", string(data))
}

func TestCopyPreserveRootMetadata(t *testing.T) {
	root, dst := t.TempDir(), t.TempDir()
	require.NoError(t, os.Chmod(root, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0644))
	stamp := time.Unix(123456789, 0)
	require.NoError(t, os.Chtimes(root, stamp, stamp))
	copier, err := NewCopier(Mount{Root: dst})
	require.NoError(t, err)
	defer copier.Close()
	require.NoError(t, copier.Copy(context.Background(), Mount{Root: root}, "/", "/", CopyOptions{CopyDirContents: true, PreserveRootMetadata: true}))
	info, err := os.Stat(dst)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0700), info.Mode().Perm())
	require.Equal(t, stamp, info.ModTime())
}
