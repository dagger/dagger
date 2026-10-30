package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/continuity/fs"
	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The overlay snapshotter the engine builds sizes a snapshot as continuity's
// fs.DiskUsage does, which is what containerd's own overlay stores: while it
// is active and once it is committed.
func TestOverlaySnapshotterUsage(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ms, err := storage.NewMetaStore(filepath.Join(root, "metadata.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ms.Close() })
	sn, name, err := newSnapshotter(filepath.Join(root, "snapshotter"), bkconfig.OCIConfig{Snapshotter: "overlayfs"}, ms)
	require.NoError(t, err)
	require.Equal(t, "overlayfs", name)
	t.Cleanup(func() { _ = sn.Close() })

	mounts, err := sn.Prepare(ctx, "active", "")
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	upper := mounts[0].Source
	writeUsageTree(t, upper, filepath.Join(root, "outside"))

	want, err := fs.DiskUsage(ctx, upper)
	require.NoError(t, err)
	require.Greater(t, want.Inodes, int64(3000))

	active, err := sn.Usage(ctx, "active")
	require.NoError(t, err)
	require.Equal(t, want.Size, active.Size)
	require.Equal(t, want.Inodes, active.Inodes)

	require.NoError(t, sn.Commit(ctx, "committed", "active"))
	committed, err := sn.Usage(ctx, "committed")
	require.NoError(t, err)
	require.Equal(t, want.Size, committed.Size)
	require.Equal(t, want.Inodes, committed.Inodes)

	// The point of the engine's walk: through the snapshotter the engine
	// builds, sizing an active snapshot and committing one allocate a
	// fraction of what continuity's walk of the same tree does.
	allocated := func(f func()) uint64 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		f()
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	mounts, err = sn.Prepare(ctx, "active2", "")
	require.NoError(t, err)
	writeUsageTree(t, mounts[0].Source, filepath.Join(root, "outside2"))
	theirs := allocated(func() { _, err = fs.DiskUsage(ctx, mounts[0].Source) })
	require.NoError(t, err)
	usage := allocated(func() { _, err = sn.Usage(ctx, "active2") })
	require.NoError(t, err)
	require.Less(t, usage*4, theirs, "Usage allocated %d bytes, fs.DiskUsage %d", usage, theirs)
	commit := allocated(func() { err = sn.Commit(ctx, "committed2", "active2") })
	require.NoError(t, err)
	require.Less(t, commit*4, theirs, "Commit allocated %d bytes, fs.DiskUsage %d", commit, theirs)
}

// writeUsageTree fills dir with what an upper directory can hold: nested and
// empty directories, a directory larger than one read of its entries, a file
// linked twice in the tree and one linked from outside it, a sparse file,
// symlinks and a fifo.
func writeUsageTree(t *testing.T, dir, outside string) {
	t.Helper()
	write := func(path string, data []byte) {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, data, 0o644))
	}
	for i := range 3000 {
		write(filepath.Join(dir, "many", fmt.Sprintf("entry-with-a-longer-name-%04d", i)), []byte{byte(i)})
	}
	write(filepath.Join(dir, "a", "b", "c", "data"), make([]byte, 100<<10))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "empty"), 0o755))
	require.NoError(t, os.Link(filepath.Join(dir, "a", "b", "c", "data"), filepath.Join(dir, "a", "link")))
	require.NoError(t, os.Link(filepath.Join(dir, "a", "b", "c", "data"), filepath.Join(dir, "link")))
	write(outside, make([]byte, 20<<10))
	require.NoError(t, os.Link(outside, filepath.Join(dir, "a", "from-outside")))

	sparse, err := os.Create(filepath.Join(dir, "sparse"))
	require.NoError(t, err)
	_, err = sparse.WriteAt([]byte("end"), 10<<20)
	require.NoError(t, err)
	require.NoError(t, sparse.Close())

	require.NoError(t, os.Symlink("a/b", filepath.Join(dir, "to-dir")))
	require.NoError(t, os.Symlink("missing", filepath.Join(dir, "dangling")))
	require.NoError(t, unix.Mkfifo(filepath.Join(dir, "a", "fifo"), 0o644))
}
