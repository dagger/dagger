package snapshots_test

import (
	"context"
	"testing"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// contentHashStores runs test against a store whose manager keeps content-hash
// trees on disk, as the engine's does, and one that keeps them in memory.
func contentHashStores(t *testing.T, test func(t *testing.T, store *testutil.Store)) {
	t.Run("files", func(t *testing.T) { test(t, testutil.NewStore(t)) })
	t.Run("memory", func(t *testing.T) { test(t, testutil.NewStoreWithMemoryContentHashes(t)) })
}

func TestContentHashMetadataRetainsOnlyOwnedImmutableSnapshots(t *testing.T) {
	contentHashStores(t, func(t *testing.T, store *testutil.Store) {
		ref, owner := store.Build(t, nil, "file", "immutable")
		md := ref.(bkcache.RefMetadata)
		require.NoError(t, md.SetExternal(bkcache.ContentHashMetadataKey, []byte("immutable hash records")))

		ctx, release := operationLease(t, store)
		defer release()
		mutable, err := store.Manager.New(ctx, nil)
		require.NoError(t, err)
		defer func() { require.NoError(t, mutable.Release(context.Background())) }()
		const mutableOwner = "retained-mutable-mirror"
		require.NoError(t, store.Manager.AttachLease(ctx, mutableOwner, mutable.SnapshotID()))
		defer func() { require.NoError(t, store.Manager.RemoveLease(context.Background(), mutableOwner)) }()
		require.NoError(t, mutable.(bkcache.RefMetadata).SetExternal(bkcache.ContentHashMetadataKey, []byte("mutable hash records")))

		require.Equal(t, []bkcache.SnapshotContentHashRow{{
			SnapshotID: ref.SnapshotID(), Data: []byte("immutable hash records"),
		}}, store.Manager.PersistentMetadataRows().ContentHashes)

		// Once the immutable snapshot loses its owner, neither it nor the still
		// owned mutable mirror should contribute hash records to a checkpoint.
		require.NoError(t, store.Manager.RemoveLease(ctx, owner))
		require.Empty(t, store.Manager.PersistentMetadataRows().ContentHashes)
	})
}

func TestContentHashMetadataSurvivesUnopenedSnapshot(t *testing.T) {
	contentHashStores(t, func(t *testing.T, store *testutil.Store) {
		ctx := context.Background()
		ref, owner := store.Build(t, nil, "file", "immutable")
		want := []byte("imported hash records")
		require.NoError(t, ref.(bkcache.RefMetadata).SetExternal(bkcache.ContentHashMetadataKey, want))
		snapshotID := ref.SnapshotID()
		require.NoError(t, ref.Release(ctx))

		// Restore the owner as dagql does, without opening the snapshot. Its
		// hash records must still be included in the next checkpoint.
		for range 2 {
			store.Reload(t)
			require.NoError(t, store.Manager.AttachLease(ctx, owner, snapshotID))
			require.Equal(t, []bkcache.SnapshotContentHashRow{{
				SnapshotID: snapshotID, Data: want,
			}}, store.Manager.PersistentMetadataRows().ContentHashes)
		}

		opCtx, release := operationLease(t, store)
		defer release()
		reopened, err := store.Manager.LeaseExistingSnapshot(opCtx, snapshotID)
		require.NoError(t, err)
		defer func() { require.NoError(t, reopened.Release(ctx)) }()
		got, err := reopened.(bkcache.RefMetadata).GetExternal(bkcache.ContentHashMetadataKey)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})
}
