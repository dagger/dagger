package snapshots_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/stretchr/testify/require"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
)

func liveChanges(t *testing.T, store *testutil.Store) bkcache.LiveChangesSnapshotter {
	t.Helper()
	lcs, ok := store.Manager.(bkcache.LiveChangesSnapshotter)
	require.True(t, ok, "snapshot manager must implement LiveChangesSnapshotter")
	return lcs
}

func leasedContext(t *testing.T, store *testutil.Store) context.Context {
	t.Helper()
	ctx := context.Background()
	lease, err := store.Leases.Create(ctx, leases.WithRandomID())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Leases.Delete(context.Background(), lease) })
	return leases.WithLease(ctx, lease.ID)
}

// mountLive mounts a mutable ref read-write, the way a running container
// would, returning its merged view. It stays mounted until cleanup.
func mountLive(t *testing.T, ctx context.Context, ref bkcache.MutableRef) string {
	t.Helper()
	mounted, err := ref.Mount(ctx, false)
	require.NoError(t, err)
	mounter := bkcache.LocalMounter(mounted)
	root, err := mounter.Mount()
	require.NoError(t, err)
	t.Cleanup(func() { _ = mounter.Unmount() })
	return root
}

// readTree returns path -> contents (directories as "<dir>") of a ref.
func readTree(t *testing.T, ctx context.Context, ref bkcache.ImmutableRef) map[string]string {
	t.Helper()
	mounted, err := ref.Mount(ctx, true)
	require.NoError(t, err)
	mounter := bkcache.LocalMounter(mounted)
	root, err := mounter.Mount()
	require.NoError(t, err)
	defer func() { require.NoError(t, mounter.Unmount()) }()

	tree := map[string]string{}
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		switch {
		case info.IsDir():
			tree[rel] = "<dir>"
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			tree[rel] = "-> " + target
		default:
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			tree[rel] = string(b)
		}
		return nil
	}))
	return tree
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(files[name]), 0o644))
	}
}

// buildBase commits a snapshot containing files.
func buildBase(t *testing.T, ctx context.Context, store *testutil.Store, files map[string]string) bkcache.ImmutableRef {
	t.Helper()
	mut, err := store.Manager.New(ctx, nil)
	require.NoError(t, err)
	mounted, err := mut.Mount(ctx, false)
	require.NoError(t, err)
	mounter := bkcache.LocalMounter(mounted)
	root, err := mounter.Mount()
	require.NoError(t, err)
	writeFiles(t, root, files)
	require.NoError(t, mounter.Unmount())
	ref, err := mut.Commit(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ref.Release(context.Background()) })
	return ref
}

func TestSnapshotLiveChangesUnsupportedOnNonOverlaySnapshotter(t *testing.T) {
	t.Parallel()
	// The native snapshotter has no upperdir to read changes from; callers
	// rely on this error to fall back to another capture strategy.
	store := testutil.NewStore(t)
	ctx := leasedContext(t, store)
	base, _ := store.Build(t, nil, "a.txt", "a")

	active, err := store.Manager.New(ctx, base)
	require.NoError(t, err)
	defer active.Release(context.Background())

	_, err = liveChanges(t, store).SnapshotLiveChanges(ctx, base, active, t.TempDir())
	require.ErrorIs(t, err, bkcache.ErrLiveChangesUnsupported)
}

func TestSnapshotLiveChangesRequiresActiveRef(t *testing.T) {
	t.Parallel()
	store := testutil.NewStore(t)
	ctx := leasedContext(t, store)
	_, err := liveChanges(t, store).SnapshotLiveChanges(ctx, nil, nil, t.TempDir())
	require.Error(t, err)
}

func TestSnapshotLiveChangesOverlay(t *testing.T) {
	store := testutil.NewOverlayStore(t) // skips unless root
	ctx := leasedContext(t, store)
	lcs := liveChanges(t, store)

	base := buildBase(t, ctx, store, map[string]string{
		"keep.txt":           "keep",
		"modify.txt":         "old",
		"delete.txt":         "gone soon",
		"dir/nested.txt":     "nested",
		"replaced/old.txt":   "old dir content",
		"deldir/inner.txt":   "whole dir removed",
		"untouched/leaf.txt": "leaf",
	})

	active, err := store.Manager.New(ctx, base)
	require.NoError(t, err)
	defer active.Release(context.Background())
	live := mountLive(t, ctx, active)

	// Mutate the live mount like a shell in a running container would.
	writeFiles(t, live, map[string]string{
		"modify.txt":      "new",
		"added.txt":       "added",
		"dir/added.txt":   "added in existing dir",
		"newdir/file.txt": "added in new dir",
	})
	require.NoError(t, os.Remove(filepath.Join(live, "delete.txt")))
	require.NoError(t, os.RemoveAll(filepath.Join(live, "deldir")))
	// Replacing a directory wholesale makes overlay mark it opaque.
	require.NoError(t, os.RemoveAll(filepath.Join(live, "replaced")))
	writeFiles(t, live, map[string]string{"replaced/fresh.txt": "fresh"})
	require.NoError(t, os.Symlink("keep.txt", filepath.Join(live, "link")))

	snap, err := lcs.SnapshotLiveChanges(ctx, base, active, live)
	require.NoError(t, err)
	defer snap.Release(context.Background())

	require.Equal(t, map[string]string{
		"keep.txt":           "keep",
		"modify.txt":         "new",
		"added.txt":          "added",
		"link":               "-> keep.txt",
		"dir":                "<dir>",
		"dir/nested.txt":     "nested",
		"dir/added.txt":      "added in existing dir",
		"newdir":             "<dir>",
		"newdir/file.txt":    "added in new dir",
		"replaced":           "<dir>",
		"replaced/fresh.txt": "fresh",
		"untouched":          "<dir>",
		"untouched/leaf.txt": "leaf",
	}, readTree(t, ctx, snap))

	t.Run("snapshot is isolated from later live writes", func(t *testing.T) {
		// Files in a live upperdir can be modified in place; the snapshot
		// must have copied them, not hard-linked them.
		f, err := os.OpenFile(filepath.Join(live, "modify.txt"), os.O_APPEND|os.O_WRONLY, 0)
		require.NoError(t, err)
		_, err = f.WriteString(" and more")
		require.NoError(t, err)
		require.NoError(t, f.Close())
		require.NoError(t, os.WriteFile(filepath.Join(live, "added.txt"), []byte("rewritten"), 0o644))

		tree := readTree(t, ctx, snap)
		require.Equal(t, "new", tree["modify.txt"])
		require.Equal(t, "added", tree["added.txt"])
	})

	t.Run("base is left untouched", func(t *testing.T) {
		tree := readTree(t, ctx, base)
		require.Equal(t, "old", tree["modify.txt"])
		require.Equal(t, "gone soon", tree["delete.txt"])
		require.Equal(t, "old dir content", tree["replaced/old.txt"])
		require.NotContains(t, tree, "added.txt")
	})
}

func TestSnapshotLiveChangesOverlayScratchBase(t *testing.T) {
	store := testutil.NewOverlayStore(t) // skips unless root
	ctx := leasedContext(t, store)

	active, err := store.Manager.New(ctx, nil)
	require.NoError(t, err)
	defer active.Release(context.Background())
	live := mountLive(t, ctx, active)
	writeFiles(t, live, map[string]string{"only.txt": "from scratch", "d/x.txt": "x"})

	snap, err := liveChanges(t, store).SnapshotLiveChanges(ctx, nil, active, live)
	require.NoError(t, err)
	defer snap.Release(context.Background())

	require.Equal(t, map[string]string{
		"only.txt": "from scratch",
		"d":        "<dir>",
		"d/x.txt":  "x",
	}, readTree(t, ctx, snap))
}

func TestSnapshotLiveChangesOverlayNoChanges(t *testing.T) {
	store := testutil.NewOverlayStore(t) // skips unless root
	ctx := leasedContext(t, store)

	base := buildBase(t, ctx, store, map[string]string{"a.txt": "a"})
	active, err := store.Manager.New(ctx, base)
	require.NoError(t, err)
	defer active.Release(context.Background())
	live := mountLive(t, ctx, active)

	snap, err := liveChanges(t, store).SnapshotLiveChanges(ctx, base, active, live)
	require.NoError(t, err)
	defer snap.Release(context.Background())
	require.Equal(t, map[string]string{"a.txt": "a"}, readTree(t, ctx, snap))
}
