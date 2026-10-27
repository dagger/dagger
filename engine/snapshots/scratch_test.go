package snapshots_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// Every empty directory starts from the scratch snapshot. Its lease is
// written once, not again for every empty directory.
func TestScratchWritesItsLeaseOnce(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	scratch, err := store.Manager.Scratch(ctx)
	require.NoError(t, err)
	require.NoError(t, scratch.Release(ctx))

	var adds atomic.Int64
	store.BeforeAdd = func(_ context.Context, _ leases.Lease, resource leases.Resource) error {
		if resource.ID == scratch.SnapshotID() {
			adds.Add(1)
		}
		return nil
	}
	for range 3 {
		again, err := store.Manager.Scratch(ctx)
		require.NoError(t, err)
		require.Equal(t, scratch.SnapshotID(), again.SnapshotID())
		require.NoError(t, again.Release(ctx))
	}
	require.Zero(t, adds.Load(), "scratch lease written again")
}
