package snapshots_test

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/stretchr/testify/require"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
)

const leaseExpireLabel = "containerd.io/gc.expire"

func operationLeaseOf(t *testing.T, ctx context.Context, store *testutil.Store) leases.Lease {
	t.Helper()
	id, ok := leases.FromContext(ctx)
	require.True(t, ok)
	all, err := store.Leases.List(context.Background())
	require.NoError(t, err)
	for _, lease := range all {
		if lease.ID == id {
			return lease
		}
	}
	t.Fatalf("operation lease %s not found", id)
	return leases.Lease{}
}

// A live operation's snapshots stay protected for as long as the operation
// runs. An expiring lease stops rooting its snapshots at the deadline, so any
// collection after it would delete a long-running exec's or service's data.
func TestOperationLeaseDoesNotExpire(t *testing.T) {
	store := testutil.NewStore(t)
	scopeCtx, release, err := bkcache.WithLazyLease(context.Background(), store.Leases, bkcache.MakeTemporary)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, release(context.Background())) })
	opCtx, err := bkcache.EnsureLease(scopeCtx)
	require.NoError(t, err)

	lease := operationLeaseOf(t, opCtx, store)
	require.NotContains(t, lease.Labels, leaseExpireLabel)

	live, err := store.Manager.New(opCtx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, live.Release(context.Background())) })
	store.GC(t)
	_, err = store.Snapshots.Stat(context.Background(), live.SnapshotID())
	require.NoError(t, err, "a live operation's snapshot must survive collection")
}

// Operation leases left by an earlier process are released at startup, so
// their snapshots do not stay on disk forever.
func TestReleaseOperationLeasesAfterRestart(t *testing.T) {
	store := testutil.NewStore(t)
	scopeCtx, _, err := bkcache.WithLazyLease(context.Background(), store.Leases, bkcache.MakeTemporary)
	require.NoError(t, err)
	opCtx, err := bkcache.EnsureLease(scopeCtx)
	require.NoError(t, err)
	leftover, err := store.Manager.New(opCtx, nil)
	require.NoError(t, err)
	snapshotID := leftover.SnapshotID()
	// The process ends without releasing the ref or its operation scope.

	kept, _ := store.Build(t, nil, "kept", "kept data")

	require.NoError(t, bkcache.ReleaseOperationLeasesAfterRestart(context.Background(), store.Leases))
	store.GC(t)
	requireSnapshotGone(t, store, snapshotID)
	testutil.CheckFile(t, kept, "kept", "kept data")
}
