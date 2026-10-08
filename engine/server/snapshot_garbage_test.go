package server

import (
	"context"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/snapshots/testutil"
)

func requireSnapshotExists(t *testing.T, store *testutil.Store, snapshotID string) {
	t.Helper()
	_, err := store.Snapshots.Stat(context.Background(), snapshotID)
	require.NoError(t, err, "snapshot %s should still be on disk", snapshotID)
}

func requireSnapshotCollected(t *testing.T, store *testutil.Store, snapshotID string) {
	t.Helper()
	_, err := store.Snapshots.Stat(context.Background(), snapshotID)
	require.True(t, cerrdefs.IsNotFound(err), "snapshot %s still exists: %v", snapshotID, err)
}

func TestSnapshotGarbageCollectsReleasedSnapshots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)

	garbage := newSnapshotGarbage(store.DB)
	require.True(t, garbage.pending.Load(), "deletions by an earlier process are unknown, so the first pass must collect")

	kept, _ := store.Build(t, nil, "kept", "kept data")
	released, releasedOwner := store.Build(t, nil, "released", "released data")
	require.NoError(t, garbage.CollectIfPending(ctx))
	require.False(t, garbage.pending.Load())

	releasedID := released.SnapshotID()
	require.NoError(t, released.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, releasedOwner))
	require.True(t, garbage.pending.Load(), "removing the last lease must mark garbage pending")
	requireSnapshotExists(t, store, releasedID)

	require.NoError(t, garbage.CollectIfPending(ctx))
	require.False(t, garbage.pending.Load())
	requireSnapshotCollected(t, store, releasedID)
	testutil.CheckFile(t, kept, "kept", "kept data")
}

// A GC pass whose policies remove nothing must still reclaim released
// snapshots. Before, collection only ran after a prune removed an entry, so
// an engine whose policies never removed anything kept that data forever.
func TestGCLockedCollectsGarbageWhenPruneRemovesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	garbage := newSnapshotGarbage(store.DB)

	kept, _ := store.Build(t, nil, "kept", "kept data")
	released, releasedOwner := store.Build(t, nil, "released", "released data")
	releasedID := released.SnapshotID()
	require.NoError(t, released.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, releasedOwner))
	requireSnapshotExists(t, store, releasedID)

	srv := &Server{
		rootDir:         t.TempDir(),
		engineCache:     newGCTestCache(t),
		snapshotGarbage: garbage,
		// Every policy keeps recent entries, so the prune removes nothing.
		workerGCPolicies: []dagqlCachePrunePolicy{{All: true, KeepDuration: 72 * time.Hour, MaxUsedSpace: 1}},
	}
	require.NoError(t, srv.gcLocked(ctx, localCacheGCScheduled))
	require.False(t, garbage.pending.Load())
	requireSnapshotCollected(t, store, releasedID)
	testutil.CheckFile(t, kept, "kept", "kept data")
}

// Disabling GC stops pruning, not the collection of data nothing references.
func TestGCLockedCollectsGarbageWithGCDisabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	garbage := newSnapshotGarbage(store.DB)

	released, releasedOwner := store.Build(t, nil, "released", "released data")
	releasedID := released.SnapshotID()
	require.NoError(t, released.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, releasedOwner))

	// With gc.enabled=false there are no worker policies and structural
	// pruning is off.
	srv := &Server{
		rootDir:         t.TempDir(),
		engineCache:     newGCTestCache(t),
		snapshotGarbage: garbage,
	}
	require.NoError(t, srv.gcLocked(ctx, localCacheGCScheduled))
	requireSnapshotCollected(t, store, releasedID)
}

// An explicit prune that removes no entries still reclaims released data.
func TestExplicitPruneCollectsReleasedSnapshots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	garbage := newSnapshotGarbage(store.DB)

	released, releasedOwner := store.Build(t, nil, "released", "released data")
	releasedID := released.SnapshotID()
	require.NoError(t, released.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, releasedOwner))

	srv := &Server{rootDir: t.TempDir(), engineCache: newGCTestCache(t), snapshotGarbage: garbage}
	_, err := srv.PruneEngineLocalCacheEntries(ctx, core.EngineCachePruneOptions{})
	require.NoError(t, err)
	requireSnapshotCollected(t, store, releasedID)
}
