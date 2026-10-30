package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	ctdsnapshot "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"golang.org/x/sys/unix"
)

// overlayUsageSnapshotter is containerd's overlay snapshotter with the walk
// that sizes a snapshot's upper directory done by diskUsage instead of
// continuity's fs.DiskUsage. The overlay walks the whole upper directory at
// every commit, to store the snapshot's size, and on every Usage of an
// active snapshot; fs.DiskUsage allocates a FileInfo and a joined path per
// entry, which made it one of a cold build's larger allocators.
type overlayUsageSnapshotter struct {
	ctdsnapshot.Snapshotter
	ms   *storage.MetaStore
	root string
}

// upperPath is the overlay snapshotter's upper directory for a snapshot id.
func (o *overlayUsageSnapshotter) upperPath(id string) string {
	return filepath.Join(o.root, "snapshots", id, "fs")
}

// Commit is the overlay snapshotter's Commit, with diskUsage.
func (o *overlayUsageSnapshotter) Commit(ctx context.Context, name, key string, opts ...ctdsnapshot.Opt) error {
	return o.ms.WithTransaction(ctx, true, func(ctx context.Context) error {
		id, _, _, err := storage.GetInfo(ctx, key)
		if err != nil {
			return err
		}
		usage, err := diskUsage(ctx, o.upperPath(id))
		if err != nil {
			return err
		}
		if _, err := storage.CommitActive(ctx, key, name, usage, opts...); err != nil {
			return fmt.Errorf("failed to commit snapshot %s: %w", key, err)
		}
		return nil
	})
}

// Usage is the overlay snapshotter's Usage, with diskUsage for an active
// snapshot. A committed snapshot's usage is the one stored at commit.
func (o *overlayUsageSnapshotter) Usage(ctx context.Context, key string) (ctdsnapshot.Usage, error) {
	var (
		id    string
		info  ctdsnapshot.Info
		usage ctdsnapshot.Usage
	)
	if err := o.ms.WithTransaction(ctx, false, func(ctx context.Context) (err error) {
		id, info, usage, err = storage.GetInfo(ctx, key)
		return err
	}); err != nil {
		return ctdsnapshot.Usage{}, err
	}
	if info.Kind == ctdsnapshot.KindActive {
		return diskUsage(ctx, o.upperPath(id))
	}
	return usage, nil
}

// Cleanup keeps the overlay's Cleanup reachable: containerd's metadata store
// calls it after collecting garbage, and with asynchronous removal it is
// what removes deleted snapshots' directories.
func (o *overlayUsageSnapshotter) Cleanup(ctx context.Context) error {
	if c, ok := o.Snapshotter.(ctdsnapshot.Cleaner); ok {
		return c.Cleanup(ctx)
	}
	return nil
}

// diskUsage is continuity's fs.DiskUsage for the tree at root: the blocks
// allocated to each distinct inode, the root's included, and the number of
// distinct inodes. It reads each directory's entries into a reused buffer and
// stats them relative to the directory's descriptor, so an entry costs its
// name and no FileInfo or joined path. Only non-directories with more than
// one link are tracked to count them once: an inode with a single link, or a
// directory, can't be met twice in one tree.
func diskUsage(ctx context.Context, root string) (ctdsnapshot.Usage, error) {
	w := &usageWalker{ctx: ctx}
	if err := unix.Lstat(root, &w.st); err != nil {
		return ctdsnapshot.Usage{}, &os.PathError{Op: "lstat", Path: root, Err: err}
	}
	w.count()
	if w.st.Mode&unix.S_IFMT == unix.S_IFDIR {
		fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return ctdsnapshot.Usage{}, &os.PathError{Op: "open", Path: root, Err: err}
		}
		buf := usageBufs.Get().(*[]byte)
		defer usageBufs.Put(buf)
		w.buf = *buf
		if err := w.dir(fd, root); err != nil {
			return ctdsnapshot.Usage{}, err
		}
	}
	return ctdsnapshot.Usage{Size: w.size, Inodes: w.inodes}, nil
}

// usageBufs holds the directory-entry buffers diskUsage reads into, so a
// commit doesn't allocate one.
var usageBufs = sync.Pool{New: func() any {
	buf := make([]byte, 32<<10)
	return &buf
}}

type usageWalker struct {
	ctx context.Context
	st  unix.Stat_t
	buf []byte
	// names holds the listings of the directories being walked, one after
	// another, each directory's read in full before its subdirectories.
	names  []string
	links  map[[2]uint64]struct{}
	size   int64
	inodes int64
}

// count adds the inode last stat'ed into w.st.
func (w *usageWalker) count() {
	if w.st.Mode&unix.S_IFMT != unix.S_IFDIR && w.st.Nlink > 1 {
		key := [2]uint64{uint64(w.st.Dev), w.st.Ino} //nolint:unconvert // Dev's type varies by architecture
		if _, ok := w.links[key]; ok {
			return
		}
		if w.links == nil {
			w.links = make(map[[2]uint64]struct{})
		}
		w.links[key] = struct{}{}
	}
	w.inodes++
	w.size += w.st.Blocks * 512
}

// dir counts the entries of the directory open as fd, and closes it.
func (w *usageWalker) dir(fd int, path string) (rerr error) {
	defer func() {
		rerr = errors.Join(rerr, unix.Close(fd))
	}()
	if err := w.ctx.Err(); err != nil {
		return err
	}
	start := len(w.names)
	defer func() { w.names = w.names[:start] }()
	for {
		n, err := unix.Getdents(fd, w.buf)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return &os.PathError{Op: "readdirent", Path: path, Err: err}
		}
		if n <= 0 {
			break
		}
		_, _, w.names = unix.ParseDirent(w.buf[:n], -1, w.names)
	}
	// Subdirectories append their own listings to w.names, which may move it;
	// this directory's names stay valid in the old array.
	names := w.names[start:]
	for _, name := range names {
		if err := unix.Fstatat(fd, name, &w.st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return &os.PathError{Op: "lstat", Path: filepath.Join(path, name), Err: err}
		}
		w.count()
		if w.st.Mode&unix.S_IFMT != unix.S_IFDIR {
			continue
		}
		sub := filepath.Join(path, name)
		cfd, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return &os.PathError{Op: "open", Path: sub, Err: err}
		}
		if err := w.dir(cfd, sub); err != nil {
			return err
		}
	}
	return nil
}
