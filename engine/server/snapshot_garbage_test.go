package server

import (
	"context"
	"errors"
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

	garbage := newSnapshotGarbage(store.DB, store.Manager)
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

	_, ok, err := store.Manager.SnapshotRecordMetadata(ctx, releasedID)
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, garbage.CollectIfPending(ctx))
	require.False(t, garbage.pending.Load())
	requireSnapshotCollected(t, store, releasedID)
	testutil.CheckFile(t, kept, "kept", "kept data")

	// The collection also drops the manager's in-memory metadata of the
	// collected snapshot, and only of it.
	_, ok, err = store.Manager.SnapshotRecordMetadata(ctx, releasedID)
	require.NoError(t, err)
	require.False(t, ok, "metadata of a collected snapshot must be dropped")
	_, ok, err = store.Manager.SnapshotRecordMetadata(ctx, kept.SnapshotID())
	require.NoError(t, err)
	require.True(t, ok)
}

// Forgetting walks every snapshot, so a collection runs it only when it
// removes a snapshot, and the first collection always.
func TestSnapshotGarbageForgetsOnlyAfterRemovals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	garbage := newSnapshotGarbage(store.DB, store.Manager)
	forget := garbage.forget
	forgets := 0
	garbage.forget = func(ctx context.Context) error {
		forgets++
		return forget(ctx)
	}

	require.NoError(t, garbage.Collect(ctx))
	require.Equal(t, 1, forgets, "the first collection forgets what an earlier process left")
	require.NoError(t, garbage.Collect(ctx))
	require.Equal(t, 1, forgets, "a collection that removes no snapshot has nothing to forget")

	released, releasedOwner := store.Build(t, nil, "released", "released data")
	releasedID := released.SnapshotID()
	require.NoError(t, released.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, releasedOwner))
	require.NoError(t, garbage.Collect(ctx))
	requireSnapshotCollected(t, store, releasedID)
	require.Equal(t, 2, forgets)
	_, ok, err := store.Manager.SnapshotRecordMetadata(ctx, releasedID)
	require.NoError(t, err)
	require.False(t, ok)
}

// A failure to forget does not fail the collection, which has already
// reclaimed the data: the prune or GC pass that ran it succeeds, and the next
// collection forgets again, even when it removes no snapshot.
func TestSnapshotGarbageRetriesFailedForget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	garbage := newSnapshotGarbage(store.DB, store.Manager)
	forget := garbage.forget
	forgets := 0
	fail := true
	garbage.forget = func(ctx context.Context) error {
		forgets++
		if fail {
			return errors.New("walk failed")
		}
		return forget(ctx)
	}
	require.NoError(t, garbage.Collect(ctx))
	require.Equal(t, 1, forgets)

	released, releasedOwner := store.Build(t, nil, "released", "released data")
	releasedID := released.SnapshotID()
	require.NoError(t, released.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, releasedOwner))
	require.NoError(t, garbage.CollectIfPending(ctx), "a failed forget must not fail the collection")
	require.Equal(t, 2, forgets)
	requireSnapshotCollected(t, store, releasedID)
	require.False(t, garbage.pending.Load())
	require.True(t, garbage.forgetPending.Load(), "a failed forget must be retried")
	_, ok, err := store.Manager.SnapshotRecordMetadata(ctx, releasedID)
	require.NoError(t, err)
	require.True(t, ok, "the failed forget dropped nothing")

	// The next collection removes no snapshot, but forgets again.
	fail = false
	require.NoError(t, garbage.Collect(ctx))
	require.Equal(t, 3, forgets)
	require.False(t, garbage.forgetPending.Load())
	_, ok, err = store.Manager.SnapshotRecordMetadata(ctx, releasedID)
	require.NoError(t, err)
	require.False(t, ok)
}

// A GC pass whose policies remove nothing must still reclaim released
// snapshots. Before, collection only ran after a prune removed an entry, so
// an engine whose policies never removed anything kept that data forever.
func TestGCLockedCollectsGarbageWhenPruneRemovesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	garbage := newSnapshotGarbage(store.DB, store.Manager)

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
	garbage := newSnapshotGarbage(store.DB, store.Manager)

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
	garbage := newSnapshotGarbage(store.DB, store.Manager)

	released, releasedOwner := store.Build(t, nil, "released", "released data")
	releasedID := released.SnapshotID()
	require.NoError(t, released.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, releasedOwner))

	srv := &Server{rootDir: t.TempDir(), engineCache: newGCTestCache(t), snapshotGarbage: garbage}
	_, err := srv.PruneEngineLocalCacheEntries(ctx, core.EngineCachePruneOptions{})
	require.NoError(t, err)
	requireSnapshotCollected(t, store, releasedID)
}
