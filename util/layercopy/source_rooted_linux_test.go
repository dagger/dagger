//go:build linux

package layercopy

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/continuity/sysx"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// A symlink hidden by an upper directory must not be followed through a raw
// lower-layer path when the target does not exist in the mounted view.
func TestFollowSymlinksDoesNotReadHiddenOverlaySymlink(t *testing.T) {
	t.Parallel()
	lower, upper, view, outside, dst := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("outside secret"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(lower, "masked")))
	for _, root := range []string{upper, view} {
		require.NoError(t, os.Mkdir(filepath.Join(root, "masked"), 0755))
		require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0755))
		require.NoError(t, os.Symlink("/masked/secret", filepath.Join(root, "src/link")))
	}
	copier, err := NewCopier(Mount{Root: dst})
	require.NoError(t, err)
	defer copier.Close()
	err = copier.Copy(context.Background(), Mount{Root: view, Mount: &mount.Mount{
		Type: "overlay", Options: []string{"lowerdir=" + upper + ":" + lower},
	}}, "/src", "/out", CopyOptions{CopyDirContents: true, FollowSymlinks: true})
	require.Error(t, err, "a missing mounted target must not expose a host file through a hidden layer symlink")
	require.NoFileExists(t, filepath.Join(dst, "out/link"))
}

func TestFollowSymlinksPinsContentsAndXattrs(t *testing.T) {
	t.Parallel()
	root, dst, outside := t.TempDir(), t.TempDir(), t.TempDir()
	file := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(file, []byte("inside"), 0640))
	require.NoError(t, sysx.LSetxattr(file, "user.follow-test", []byte("inside attribute"), 0))
	external := filepath.Join(outside, "secret")
	require.NoError(t, os.WriteFile(external, []byte("outside secret"), 0600))
	require.NoError(t, sysx.LSetxattr(external, "user.follow-test", []byte("outside attribute"), 0))
	src, err := newSource(Mount{Root: root})
	require.NoError(t, err)
	require.NoError(t, src.confine())
	defer src.rootHandle.Close()
	require.NoError(t, src.selectBase("/", true))
	ent, err := src.pinEntry(sourceEntry{Rel: "file", ViewPath: file})
	require.NoError(t, err)
	defer ent.file.Close()
	// Replacing the pathname after validation must not change the copied inode.
	require.NoError(t, os.Rename(file, file+".saved"))
	require.NoError(t, os.Symlink(external, file))
	copier, err := NewCopier(Mount{Root: dst})
	require.NoError(t, err)
	defer copier.Close()
	require.NoError(t, copier.copyNode(ent, "/copied", resolvedParent{}, CopyOptions{DisableSourceHardlinks: true}))
	data, err := os.ReadFile(filepath.Join(dst, "copied"))
	require.NoError(t, err)
	require.Equal(t, "inside", string(data))
	attr, err := sysx.LGetxattr(filepath.Join(dst, "copied"), "user.follow-test")
	require.NoError(t, err)
	require.Equal(t, "inside attribute", string(attr))
}

func TestFollowSymlinksConfinesDirectoryReplacement(t *testing.T) {
	t.Parallel()
	root, dst, outside := t.TempDir(), t.TempDir(), t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "dir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("outside secret"), 0600))
	src, err := newSource(Mount{Root: root})
	require.NoError(t, err)
	require.NoError(t, src.confine())
	defer src.rootHandle.Close()
	src.onReadDir = func(rel string) {
		if rel != "dir" {
			return
		}
		require.NoError(t, os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "saved")))
		require.NoError(t, os.Symlink(outside, filepath.Join(root, "dir")))
	}
	copier, err := NewCopier(Mount{Root: dst})
	require.NoError(t, err)
	defer copier.Close()
	matcher, err := newMatcher(root, Filter{})
	require.NoError(t, err)
	err = copier.copy(context.Background(), src, matcher, "/", "/out", CopyOptions{CopyDirContents: true, FollowSymlinks: true, DisableSourceHardlinks: true})
	require.Error(t, err)
	require.NoFileExists(t, filepath.Join(dst, "out/dir/secret"))
}

func TestFollowSymlinksPinsRootAcrossRename(t *testing.T) {
	t.Parallel()
	parent, outside := t.TempDir(), t.TempDir()
	root := filepath.Join(parent, "root")
	require.NoError(t, os.Mkdir(root, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("inside"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file"), []byte("outside secret"), 0600))
	src, err := newSource(Mount{Root: root})
	require.NoError(t, err)
	require.NoError(t, src.confine())
	defer src.rootHandle.Close()
	require.NoError(t, os.Rename(root, filepath.Join(parent, "saved")))
	require.NoError(t, os.Symlink(outside, root))
	f, err := src.openRooted("file", unix.O_RDONLY)
	require.NoError(t, err)
	defer f.Close()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, "inside", string(data))
}

func TestFollowSymlinksConfinesGitignore(t *testing.T) {
	t.Parallel()
	for _, fifo := range []bool{false, true} {
		t.Run(fmt.Sprintf("fifo=%t", fifo), func(t *testing.T) {
			root, dst := t.TempDir(), t.TempDir()
			rule := filepath.Join(root, ".gitignore")
			if fifo {
				require.NoError(t, unix.Mkfifo(rule, 0600))
			} else {
				external := filepath.Join(t.TempDir(), "external-ignore")
				require.NoError(t, os.WriteFile(external, []byte("*\n"), 0600))
				require.NoError(t, os.Symlink(external, rule))
			}
			require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("inside"), 0644))
			copier, err := NewCopier(Mount{Root: dst})
			require.NoError(t, err)
			defer copier.Close()
			err = copier.Copy(context.Background(), Mount{Root: root}, "/", "/out", CopyOptions{
				CopyDirContents: true, FollowSymlinks: true, Filter: Filter{Gitignore: true},
			})
			require.Error(t, err)
		})
	}
}

func TestFollowSymlinksRejectsMagicLinks(t *testing.T) {
	t.Parallel()
	outside, err := os.Open(t.TempDir())
	require.NoError(t, err)
	defer outside.Close()
	src, err := newSource(Mount{Root: "/proc"})
	require.NoError(t, err)
	require.NoError(t, src.confine())
	defer src.rootHandle.Close()
	f, err := src.openRooted(fmt.Sprintf("self/fd/%d", outside.Fd()), unix.O_PATH)
	if f != nil {
		f.Close()
	}
	require.Error(t, err)
}

func TestFollowSymlinksCopiesSourceButPreservesInternalHardlinks(t *testing.T) {
	t.Parallel()
	root, dst := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("inside"), 0644))
	require.NoError(t, os.Symlink("file", filepath.Join(root, "link")))
	copier, err := NewCopier(Mount{Root: dst})
	require.NoError(t, err)
	defer copier.Close()
	require.NoError(t, copier.Copy(context.Background(), Mount{Root: root}, "/", "/out", CopyOptions{CopyDirContents: true, FollowSymlinks: true}))
	srcInfo, err := os.Stat(filepath.Join(root, "file"))
	require.NoError(t, err)
	fileInfo, err := os.Stat(filepath.Join(dst, "out/file"))
	require.NoError(t, err)
	linkInfo, err := os.Stat(filepath.Join(dst, "out/link"))
	require.NoError(t, err)
	require.False(t, os.SameFile(srcInfo, fileInfo))
	require.True(t, os.SameFile(fileInfo, linkInfo))
}

func TestFollowSymlinksGitignoreWithinSource(t *testing.T) {
	t.Parallel()
	root, dst := t.TempDir(), t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "target"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "target/.gitignore"), []byte("skip.txt\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "target/skip.txt"), []byte("skip"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "target/keep.txt"), []byte("keep"), 0644))
	require.NoError(t, os.Symlink("/target", filepath.Join(root, "link")))
	copier, err := NewCopier(Mount{Root: dst})
	require.NoError(t, err)
	defer copier.Close()
	require.NoError(t, copier.Copy(context.Background(), Mount{Root: root}, "/", "/out", CopyOptions{
		CopyDirContents: true, FollowSymlinks: true, Filter: Filter{Gitignore: true},
	}))
	for _, name := range []string{"link", "target"} {
		data, err := os.ReadFile(filepath.Join(dst, "out", name, "keep.txt"))
		require.NoError(t, err)
		require.Equal(t, "keep", string(data))
		require.NoFileExists(t, filepath.Join(dst, "out", name, "skip.txt"))
	}
}

func TestRootedOpenPreservesSnapshotSymlinkSemantics(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, dir := range []string{"a", "target/deep"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "target/file"), []byte("target"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("root"), 0644))
	for name, target := range map[string]string{
		"absolute":     "/target/file",
		"a/relative":   "../absolute",
		"a/clamped":    "../../../file",
		"dir":          "/target/deep",
		"mixed":        "dir/../file",
		"bad-trailing": "/target/file/",
	} {
		require.NoError(t, os.Symlink(target, filepath.Join(root, name)))
	}
	src, err := newSource(Mount{Root: root})
	require.NoError(t, err)
	require.NoError(t, src.confine())
	defer src.rootHandle.Close()
	for _, tc := range []struct{ name, want string }{
		{"absolute", "target"}, {"a/relative", "target"},
		{"a/clamped", "root"}, {"dir/../file", "target"}, {"mixed", "target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := src.openRooted(tc.name, os.O_RDONLY)
			require.NoError(t, err)
			defer f.Close()
			data, err := io.ReadAll(f)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(data))
		})
	}
	t.Run("no follow final link", func(t *testing.T) {
		f, err := src.openRooted("absolute", unix.O_PATH|unix.O_NOFOLLOW)
		require.NoError(t, err)
		defer f.Close()
		info, err := f.Stat()
		require.NoError(t, err)
		require.NotZero(t, info.Mode()&os.ModeSymlink)
	})
	for _, name := range []string{"bad-trailing", "target/file/"} {
		t.Run(name, func(t *testing.T) {
			f, err := src.openRooted(name, unix.O_PATH)
			if f != nil {
				f.Close()
			}
			require.ErrorIs(t, err, unix.ENOTDIR)
		})
	}
}

func TestRootedOpenPreservesSymlinkLimit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("inside"), 0644))
	target := "file"
	for i := range 41 {
		name := fmt.Sprintf("link-%d", i)
		require.NoError(t, os.Symlink(target, filepath.Join(root, name)))
		target = name
	}
	src, err := newSource(Mount{Root: root})
	require.NoError(t, err)
	require.NoError(t, src.confine())
	defer src.rootHandle.Close()
	f, err := src.openRooted("link-39", os.O_RDONLY)
	require.NoError(t, err)
	defer f.Close()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, "inside", string(data))
	f, err = src.openRooted("link-40", os.O_RDONLY)
	if f != nil {
		f.Close()
	}
	require.ErrorIs(t, err, unix.ELOOP)
}

// Stage the mutation after expansion and before the exact final-open code used
// by openRooted. These tests do not depend on scheduler timing to hit the gap.
func TestRootedOpenExpansionToOpenReplacement(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"leaf absolute escape", "leaf relative escape", "parent absolute escape", "parent relative escape", "leaf in-root link", "parent in-root link", "leaf in-root file"} {
		for _, flags := range []int{unix.O_PATH, os.O_RDONLY} {
			t.Run(fmt.Sprintf("%s/flags=%d", kind, flags), func(t *testing.T) {
				root, outside := t.TempDir(), t.TempDir()
				require.NoError(t, os.Mkdir(filepath.Join(root, "dir"), 0755))
				require.NoError(t, os.Mkdir(filepath.Join(root, "replacement"), 0755))
				require.NoError(t, os.WriteFile(filepath.Join(root, "dir/entry"), []byte("original"), 0644))
				require.NoError(t, os.Symlink("/dir/entry", filepath.Join(root, "alias")))
				require.NoError(t, os.WriteFile(filepath.Join(root, "dir/other"), []byte("replacement"), 0644))
				require.NoError(t, os.WriteFile(filepath.Join(root, "replacement/entry"), []byte("replacement"), 0644))
				require.NoError(t, os.WriteFile(filepath.Join(outside, "entry"), []byte("HOST SECRET"), 0600))
				src, err := newSource(Mount{Root: root})
				require.NoError(t, err)
				require.NoError(t, src.confine())
				defer src.rootHandle.Close()
				resolved, err := src.resolveRooted("alias", true)
				require.NoError(t, err)
				require.Equal(t, "dir/entry", resolved)

				parent := strings.HasPrefix(kind, "parent")
				replaced := filepath.Join(root, "dir/entry")
				if parent {
					replaced = filepath.Join(root, "dir")
				}
				require.NoError(t, os.Rename(replaced, replaced+".saved"))
				var target string
				switch kind {
				case "leaf absolute escape":
					target = filepath.Join(outside, "entry")
				case "leaf relative escape":
					target, err = filepath.Rel(filepath.Dir(replaced), filepath.Join(outside, "entry"))
					require.NoError(t, err)
				case "parent absolute escape":
					target = outside
				case "parent relative escape":
					target, err = filepath.Rel(root, outside)
					require.NoError(t, err)
				case "leaf in-root link":
					target = "other"
				case "parent in-root link":
					target = "replacement"
				case "leaf in-root file":
					require.NoError(t, os.WriteFile(replaced, []byte("replacement"), 0644))
				}
				if target != "" {
					require.NoError(t, os.Symlink(target, replaced))
				}

				f, err := src.openResolvedRooted("alias", resolved, flags)
				if f != nil {
					defer f.Close()
				}
				if strings.Contains(kind, "escape") || kind == "leaf in-root link" && flags == unix.O_PATH {
					require.Error(t, err)
					require.Nil(t, f)
					return
				}
				require.NoError(t, err)
				info, err := f.Stat()
				require.NoError(t, err)
				require.True(t, info.Mode().IsRegular())
				// Read only after establishing that this is a pinned regular inode.
				readable, err := os.Open(fmt.Sprintf("/proc/self/fd/%d", f.Fd()))
				require.NoError(t, err)
				defer readable.Close()
				data, err := io.ReadAll(readable)
				require.NoError(t, err)
				require.Equal(t, "replacement", string(data))
			})
		}
	}
}

func TestRootedOpenFinalTypeReplacement(t *testing.T) {
	t.Parallel()
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprintf("directory=%t", directory), func(t *testing.T) {
			root := t.TempDir()
			entry := filepath.Join(root, "entry")
			if directory {
				require.NoError(t, os.Mkdir(entry, 0755))
			} else {
				require.NoError(t, os.WriteFile(entry, []byte("original"), 0644))
			}
			src, err := newSource(Mount{Root: root})
			require.NoError(t, err)
			require.NoError(t, src.confine())
			defer src.rootHandle.Close()
			resolved, err := src.resolveRooted("entry", true)
			require.NoError(t, err)
			require.NoError(t, os.Rename(entry, entry+".saved"))
			require.NoError(t, unix.Mkfifo(entry, 0600))
			flags := unix.O_PATH
			if directory {
				flags = os.O_RDONLY | unix.O_DIRECTORY
			}
			f, err := src.openResolvedRooted("entry", resolved, flags)
			if f != nil {
				defer f.Close()
			}
			if directory {
				require.ErrorIs(t, err, unix.ENOTDIR)
				require.Nil(t, f)
			} else {
				require.NoError(t, err)
				info, err := f.Stat()
				require.NoError(t, err)
				require.NotZero(t, info.Mode()&os.ModeNamedPipe)
			}
		})
	}
}
