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
	copier, dst := selectionCopier(t)
	require.NoError(t, copier.CopySelection(context.Background(), root, name))
	return dst
}

func selectionCopier(t *testing.T) (*Copier, string) {
	t.Helper()
	dst := t.TempDir()
	copier, err := NewCopier(Mount{Root: dst})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, copier.Close()) })
	return copier, dst
}

func requireSelectionContents(t *testing.T, path, want string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, want, string(contents))
}

func selectionFixture(t *testing.T, files, links map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
	}
	for name, target := range links {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.Symlink(target, path))
	}
	return root
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
		requireSelectionContents(t, filepath.Join(dst, name, "link"), "shared")
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
	root, outside := t.TempDir(), t.TempDir()
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
	copier, dst := selectionCopier(t)
	require.NoError(t, copier.copyNode(ent, "/file", resolvedParent{}, CopyOptions{DisableSourceHardlinks: true}))
	requireSelectionContents(t, filepath.Join(dst, "file"), "inside")
	attr, err := sysx.LGetxattr(filepath.Join(dst, "file"), "user.selection")
	require.NoError(t, err)
	require.Equal(t, "inside", string(attr))
}

func TestSelectionPinnedDirectoryReplacement(t *testing.T) {
	root := selectionFixture(t, map[string]string{"src/file": "inside"}, nil)
	outside := selectionFixture(t, map[string]string{"file": "outside"}, nil)
	f, err := os.Open(filepath.Join(root, "src"))
	require.NoError(t, err)
	defer f.Close()
	ent, err := pinnedSelectionEntry(f)
	require.NoError(t, err)
	require.NoError(t, os.Rename(filepath.Join(root, "src"), filepath.Join(root, "saved")))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "src")))
	copier, dst := selectionCopier(t)
	copy := selectionCopy{copier: copier, directories: map[string]string{}}
	require.NoError(t, copy.directory(context.Background(), ent, "src", "/", "src"))
	requireSelectionContents(t, filepath.Join(dst, "file"), "inside")
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

func TestSelectionResolvesDotDotAfterReusedDirectory(t *testing.T) {
	for _, alias := range []string{"alias", "n-alias"} {
		for _, decoy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/decoy=%t", alias, decoy), func(t *testing.T) {
				root := t.TempDir()
				require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0755))
				require.NoError(t, os.MkdirAll(filepath.Join(root, "shared/other"), 0755))
				require.NoError(t, os.WriteFile(filepath.Join(root, "shared/file"), []byte("correct"), 0644))
				if decoy {
					require.NoError(t, os.WriteFile(filepath.Join(root, "src/file"), []byte("wrong"), 0644))
				}
				for name, target := range map[string]string{
					"src/a":              "../shared/other",
					"src/b":              "../shared",
					"src/link":           "a/../file",
					"shared/" + alias:    "/shared/other",
					"shared/link":        alias + "/../file",
					"shared/dangling":    alias + "/../missing",
					"shared/other/local": "../file",
				} {
					require.NoError(t, os.Symlink(target, filepath.Join(root, name)))
				}
				dst := copySelection(t, root, "src")
				requireSelectionContents(t, filepath.Join(dst, "link"), "correct")
				requireSelectionContents(t, filepath.Join(dst, "b/link"), "correct")
				// Check both traversal orders: link can precede the alias that
				// will later be redirected to a directory outside b.
				for name, target := range map[string]string{
					"b/dangling":    alias + "/../missing",
					"b/other/local": "../file",
				} {
					actual, err := os.Readlink(filepath.Join(dst, name))
					require.NoError(t, err)
					require.Equal(t, target, actual)
				}
			})
		}
	}
}

func TestSelectionKeepsInternalDirectoryTargetsInPlace(t *testing.T) {
	for _, alias := range []string{"alias", "zzalias"} {
		for _, parent := range []string{"src", "src/deep"} {
			t.Run(alias+"/"+parent, func(t *testing.T) {
				root := t.TempDir()
				require.NoError(t, os.MkdirAll(filepath.Join(root, parent, "zdir"), 0755))
				require.NoError(t, os.WriteFile(filepath.Join(root, parent, "value"), []byte("original"), 0644))
				require.NoError(t, os.Symlink("../value", filepath.Join(root, parent, "zdir/link")))
				require.NoError(t, os.Symlink("../"+parent+"/zdir", filepath.Join(root, "src", alias)))
				dst := copySelection(t, root, "src")
				rel, err := filepath.Rel("src", parent)
				require.NoError(t, err)
				require.NoError(t, os.Remove(filepath.Join(dst, rel, "value")))
				require.NoError(t, os.WriteFile(filepath.Join(dst, rel, "value"), []byte("updated"), 0644))
				for _, name := range []string{alias + "/link", filepath.Join(rel, "zdir/link")} {
					actual, err := os.Readlink(filepath.Join(dst, name))
					require.NoError(t, err)
					require.Equal(t, "../value", actual)
					requireSelectionContents(t, filepath.Join(dst, name), "updated")
				}
			})
		}
	}
}

func TestSelectionPreservesLocalDotDotLinks(t *testing.T) {
	for _, selection := range []string{".", "src"} {
		t.Run(selection, func(t *testing.T) {
			links := map[string]string{}
			readable := map[string]string{}
			for _, alias := range []string{"alias", "z-alias"} {
				links["src/"+alias] = "dir"
				links["src/chain-"+alias] = alias
				readable["link-"+alias] = alias + "/../file"
				readable["multi-"+alias] = "chain-" + alias + "/../" + alias + "/../file"
			}
			for _, count := range []int{8, 9, 39} {
				target := "dir"
				for i := range count {
					name := fmt.Sprintf("chain-%d-%d", count, i)
					links["src/"+name] = target
					target = name
				}
				readable[fmt.Sprintf("long-%d", count)] = target + "/../file"
			}
			for name, target := range readable {
				links["src/"+name] = target
			}
			root := selectionFixture(t, map[string]string{"src/file": "original"}, links)
			require.NoError(t, os.Mkdir(filepath.Join(root, "src/dir"), 0755))
			for name := range readable {
				requireSelectionContents(t, filepath.Join(root, "src", name), "original")
			}
			dst := copySelection(t, root, selection)
			// Every link keeps its text, including links beyond os.Root's
			// traversal limit and aliases before or after their users.
			for name, target := range links {
				if selection == "src" {
					name = name[len("src/"):]
				}
				actual, err := os.Readlink(filepath.Join(dst, name))
				require.NoError(t, err)
				require.Equal(t, target, actual)
			}
			if selection == "." {
				dst = filepath.Join(dst, "src")
			}
			require.NoError(t, os.Remove(filepath.Join(dst, "file")))
			require.NoError(t, os.WriteFile(filepath.Join(dst, "file"), []byte("replacement"), 0644))
			for name := range readable {
				requireSelectionContents(t, filepath.Join(dst, name), "replacement")
			}
		})
	}
}

func TestSelectionPreservesSymlinkLimit(t *testing.T) {
	for _, relative := range []bool{false, true} {
		for _, links := range []int{40, 41} {
			t.Run(fmt.Sprintf("relative=%t/links=%d", relative, links), func(t *testing.T) {
				root := t.TempDir()
				require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0755))
				require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("target"), 0644))
				target := "file"
				for i := range links - 1 {
					name := fmt.Sprintf("chain-%d", i)
					require.NoError(t, os.Symlink(target, filepath.Join(root, name)))
					target = name
				}
				if relative {
					target = "../" + target
				} else {
					target = "/" + target
				}
				require.NoError(t, os.Symlink(target, filepath.Join(root, "src/link")))
				if relative {
					// Check the kernel's limit before selection changes the tree.
					content, err := os.ReadFile(filepath.Join(root, "src/link"))
					if links == 40 {
						require.NoError(t, err)
						require.Equal(t, "target", string(content))
					} else {
						require.ErrorIs(t, err, unix.ELOOP)
					}
				}
				dst := copySelection(t, root, "src")
				if links == 40 {
					requireSelectionContents(t, filepath.Join(dst, "link"), "target")
				} else {
					actual, err := os.Readlink(filepath.Join(dst, "link"))
					require.NoError(t, err)
					require.Equal(t, target, actual)
				}
			})
		}
	}
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
	root := selectionFixture(t, map[string]string{"file": "inside"}, map[string]string{"src/link": "../file"})
	name := filepath.Join(root, "src/link")
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
	src := rootedSource(t, root)
	copier, dst := selectionCopier(t)
	copy := selectionCopy{copier: copier, source: src, directories: map[string]string{}}
	require.NoError(t, copy.link(context.Background(), ent, "src/link", "/link", "src"))
	requireSelectionContents(t, filepath.Join(dst, "link"), "inside")
}
