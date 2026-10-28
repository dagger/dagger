package snapshots_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// A layer's blob is attached to the leases owning its snapshot once: an
// export that reuses the layer writes nothing for its owners, however many
// there are. An owner added after the blob was recorded gets it from
// AttachLease, as does one removed and attached again, and a blob recorded
// for the first time is attached to the owners its snapshot has then.
func TestExportReusedLayerWritesNoOwnerLeases(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	var commits atomic.Int64
	store.DB.RegisterMutationCallback(func(bool) { commits.Add(1) })
	parent, parentOwner := buildSnapshot(t, store, nil, "p.txt", "owned parent")
	child, childOwner := buildSnapshot(t, store, parent, "c.txt", "owned child")
	owners := []string{parentOwner, childOwner}
	t.Cleanup(func() {
		require.NoError(t, child.Release(ctx))
		require.NoError(t, parent.Release(ctx))
		for _, owner := range owners {
			require.NoError(t, store.Manager.RemoveLease(ctx, owner))
		}
	})
	// export exports ref's chain and releases it, and returns its blobs
	// and the metadata commits it made.
	export := func(ref bkcache.ImmutableRef) ([]string, int64) {
		t.Helper()
		before := commits.Load()
		chain, err := ref.ExportChain(ctx, uncompressed)
		require.NoError(t, err)
		require.NoError(t, chain.Release(ctx))
		var blobs []string
		for _, layer := range chain.Layers {
			blobs = append(blobs, layer.Descriptor.Digest.String())
		}
		return blobs, commits.Load() - before
	}
	owns := func(owner string, blobs []string) {
		t.Helper()
		_, _, contents := leaseResources(t, store, owner)
		require.Subset(t, contents, blobs, "owner %s has the blobs of the snapshots it owns", owner)
	}

	blobs, _ := export(child) // diffs both layers and records their blobs
	require.Len(t, blobs, 2)
	_, base := export(child)

	for i := range 50 {
		owner := fmt.Sprintf("dagql/result/owner-%d", i)
		require.NoError(t, store.Manager.AttachLease(ctx, owner, child.SnapshotID()))
		owners = append(owners, owner)
		owns(owner, blobs)
	}
	_, again := export(child)
	require.Equal(t, base, again, "fifty more owners of the reused layers make no more metadata writes")
	for _, owner := range owners {
		owns(owner, blobs)
	}

	removed := owners[len(owners)-1]
	require.NoError(t, store.Manager.RemoveLease(ctx, removed))
	require.NoError(t, store.Manager.AttachLease(ctx, removed, child.SnapshotID()))
	owns(removed, blobs)
	_, again = export(child)
	require.Equal(t, base, again)
	owns(removed, blobs)

	grandchild, grandOwner := buildSnapshot(t, store, child, "g.txt", "owned grandchild")
	owners = append(owners, grandOwner)
	t.Cleanup(func() { require.NoError(t, grandchild.Release(ctx)) })
	const early = "dagql/result/before-its-blob"
	require.NoError(t, store.Manager.AttachLease(ctx, early, grandchild.SnapshotID()))
	owners = append(owners, early)
	grandBlobs, _ := export(grandchild)
	require.Len(t, grandBlobs, 3)
	owns(early, grandBlobs)
	owns(grandOwner, grandBlobs)
}
