//go:build linux

package layercopy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func rootedSource(t *testing.T, root string) *source {
	t.Helper()
	src, err := newSource(Mount{Root: root})
	require.NoError(t, err)
	require.NoError(t, src.confine())
	t.Cleanup(func() { require.NoError(t, src.rootHandle.Close()) })
	return src
}

func TestFollowSymlinksPinsRootAcrossRename(t *testing.T) {
	t.Parallel()
	parent, outside := t.TempDir(), t.TempDir()
	root := filepath.Join(parent, "root")
	require.NoError(t, os.Mkdir(root, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), []byte("inside"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file"), []byte("outside secret"), 0600))
	src := rootedSource(t, root)
	require.NoError(t, os.Rename(root, filepath.Join(parent, "saved")))
	require.NoError(t, os.Symlink(outside, root))
	f, err := src.openRooted("file", unix.O_RDONLY)
	require.NoError(t, err)
	defer f.Close()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	require.Equal(t, "inside", string(data))
}

func TestFollowSymlinksRejectsMagicLinks(t *testing.T) {
	t.Parallel()
	outside, err := os.Open(t.TempDir())
	require.NoError(t, err)
	defer outside.Close()
	src := rootedSource(t, "/proc")
	f, err := src.openRooted(fmt.Sprintf("self/fd/%d", outside.Fd()), unix.O_PATH)
	if f != nil {
		f.Close()
	}
	require.Error(t, err)
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
	target := "target/file"
	for i := range 41 {
		name := fmt.Sprintf("link-%d", i)
		require.NoError(t, os.Symlink(target, filepath.Join(root, name)))
		target = name
	}
	src := rootedSource(t, root)
	for _, tc := range []struct{ name, want string }{
		{"absolute", "target"}, {"a/relative", "target"},
		{"a/clamped", "root"}, {"dir/../file", "target"}, {"mixed", "target"},
		{"link-39", "target"}, // 40 links are allowed.
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
	for _, tc := range []struct {
		name string
		want error
	}{
		{"bad-trailing", unix.ENOTDIR}, {"target/file/", unix.ENOTDIR},
		{"link-40", unix.ELOOP}, // 41 links exceed the limit.
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := src.openRooted(tc.name, unix.O_PATH)
			if f != nil {
				f.Close()
			}
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// Stage the mutation after expansion and before the exact final-open code used
// by openRooted. These tests do not depend on scheduler timing to hit the gap.
func TestRootedOpenExpansionToOpenReplacement(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"leaf absolute escape", "leaf relative escape", "parent absolute escape", "parent relative escape", "leaf in-root link", "parent in-root link", "leaf in-root file", "leaf fifo", "directory fifo"} {
		flags := []int{unix.O_PATH, os.O_RDONLY}
		switch kind {
		case "leaf fifo":
			flags = []int{unix.O_PATH}
		case "directory fifo":
			flags = []int{os.O_RDONLY | unix.O_DIRECTORY}
		}
		for _, flags := range flags {
			t.Run(fmt.Sprintf("%s/flags=%d", kind, flags), func(t *testing.T) {
				root := selectionFixture(t, map[string]string{
					"dir/entry": "original", "dir/other": "replacement", "replacement/entry": "replacement",
				}, map[string]string{"alias": "/dir/entry"})
				outside := selectionFixture(t, map[string]string{"entry": "HOST SECRET"}, nil)
				if kind == "directory fifo" {
					require.NoError(t, os.Remove(filepath.Join(root, "dir/entry")))
					require.NoError(t, os.Mkdir(filepath.Join(root, "dir/entry"), 0755))
				}
				src := rootedSource(t, root)
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
				case "leaf fifo", "directory fifo":
					require.NoError(t, unix.Mkfifo(replaced, 0600))
				}
				if target != "" {
					require.NoError(t, os.Symlink(target, replaced))
				}

				f, err := src.openResolvedRooted("alias", resolved, flags)
				if f != nil {
					defer f.Close()
				}
				if kind == "directory fifo" {
					require.ErrorIs(t, err, unix.ENOTDIR)
					require.Nil(t, f)
					return
				}
				if strings.Contains(kind, "escape") || kind == "leaf in-root link" && flags == unix.O_PATH {
					require.Error(t, err)
					require.Nil(t, f)
					return
				}
				require.NoError(t, err)
				info, err := f.Stat()
				require.NoError(t, err)
				if kind == "leaf fifo" {
					require.NotZero(t, info.Mode()&os.ModeNamedPipe)
					return
				}
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
