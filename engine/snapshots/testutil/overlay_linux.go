package testutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// NewOverlayStore builds a store on containerd's overlay snapshotter and its
// own applier, as the engine runs: chain imports onto it apply their layers
// apart and rebase them. The applier writes an overlay snapshot's directory
// in place and mounts nothing, so imports of layers with no whiteouts need
// no privileges.
//
// Whiteouts are device nodes, which an overlay filesystem such as a test
// container's own /tmp refuses to hold; with the privilege to mount, the
// store is then kept on a tmpfs, as an engine keeps its state on a
// filesystem of its own. CanMakeWhiteouts and CanMakeOpaque say whether the
// store can hold whiteouts and opaque directories. Only importing onto it is
// supported: Build, Root and diffs expect a native store's directories.
// OverlayTree reads what it imported.
func NewOverlayStore(t testing.TB) *Store {
	t.Helper()
	root := t.TempDir()
	if makeWhiteout(root) != nil && unix.Mount("tmpfs", root, "tmpfs", 0, "") == nil {
		t.Cleanup(func() { require.NoError(t, unix.Unmount(root, 0)) })
	}
	return newStore(t, root, "overlayfs", func(root string) (ctdsnapshots.Snapshotter, error) {
		return overlay.NewSnapshotter(root)
	})
}

const opaqueXattr = "trusted.overlay.opaque"

func makeWhiteout(dir string) error {
	path := filepath.Join(dir, "whiteout-probe")
	if err := unix.Mknod(path, unix.S_IFCHR, 0); err != nil {
		return err
	}
	return os.Remove(path)
}

// CanMakeWhiteouts reports why the store can't hold the device nodes overlay
// whiteouts are, or nil when it can, and CanMakeOpaque why it can't mark a
// directory opaque, which takes CAP_SYS_ADMIN.
func (s *Store) CanMakeWhiteouts() error {
	return makeWhiteout(s.root)
}

func (s *Store) CanMakeOpaque() error {
	dir, err := os.MkdirTemp(s.root, "opaque-probe")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	return unix.Setxattr(dir, opaqueXattr, []byte("y"), 0)
}

// OverlayTree returns the tree a committed snapshot of an overlay store
// shows, merged from its layers' directories as overlayfs merges them,
// without mounting: each path, with its kind and permissions, a file's
// content and a symlink's target. A whiteout hides the path below it, and
// an opaque directory everything below it.
func OverlayTree(t testing.TB, ref bkcache.ImmutableRef) map[string]string {
	t.Helper()
	mounted, err := ref.Mount(context.Background(), true)
	require.NoError(t, err)
	mounts, release, err := mounted.Mount()
	require.NoError(t, err)
	if release != nil {
		defer func() { require.NoError(t, release()) }()
	}
	tree := map[string]string{}
	for _, layer := range overlayLayers(t, mounts) {
		require.NoError(t, filepath.Walk(layer, func(path string, info os.FileInfo, err error) error {
			if err != nil || path == layer {
				return err
			}
			rel, err := filepath.Rel(layer, path)
			if err != nil {
				return err
			}
			hide := func() {
				for p := range tree {
					if strings.HasPrefix(p, rel+"/") {
						delete(tree, p)
					}
				}
			}
			mode := info.Mode()
			switch {
			case mode&os.ModeCharDevice != 0 && info.Sys().(*syscall.Stat_t).Rdev == 0:
				delete(tree, rel)
				hide()
			case mode.IsDir():
				if opaque, err := isOpaque(path); err != nil {
					return err
				} else if opaque {
					hide()
				}
				tree[rel] = fmt.Sprintf("dir %o", mode.Perm())
			case mode&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				hide()
				tree[rel] = "symlink " + target
			case mode.IsRegular():
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				hide()
				tree[rel] = fmt.Sprintf("file %o %s", mode.Perm(), data)
			default:
				return fmt.Errorf("unexpected %s in layer %s", mode.Type(), layer)
			}
			return nil
		}))
	}
	return tree
}

// overlayLayers returns the directories of an overlay snapshot's view,
// lowest first: one bind mount's source, or an overlay mount's lowers.
func overlayLayers(t testing.TB, mounts []mount.Mount) []string {
	t.Helper()
	require.Len(t, mounts, 1)
	switch mounts[0].Type {
	case "bind":
		return []string{mounts[0].Source}
	case "overlay":
		for _, o := range mounts[0].Options {
			if lowers, ok := strings.CutPrefix(o, "lowerdir="); ok {
				layers := strings.Split(lowers, ":")
				slices.Reverse(layers)
				return layers
			}
		}
	}
	t.Fatalf("unexpected overlay view mounts %+v", mounts)
	return nil
}

func isOpaque(path string) (bool, error) {
	buf := make([]byte, 1)
	n, err := unix.Getxattr(path, opaqueXattr, buf)
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.EPERM) {
		return false, nil
	}
	return err == nil && string(buf[:n]) == "y", err
}
