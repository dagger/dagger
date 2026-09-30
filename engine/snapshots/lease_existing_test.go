package snapshots_test

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	cerrdefs "github.com/containerd/errdefs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

func operationLease(t *testing.T, store *testutil.Store) (context.Context, func()) {
	t.Helper()
	ctx, release, err := bkcache.WithLazyLease(context.Background(), store.Leases, bkcache.MakeTemporary)
	require.NoError(t, err)
	return ctx, func() { require.NoError(t, release(context.Background())) }
}

func dropOwner(t *testing.T, store *testutil.Store, ref bkcache.ImmutableRef, owner string) {
	t.Helper()
	require.NoError(t, ref.Release(context.Background()))
	require.NoError(t, store.Manager.RemoveLease(context.Background(), owner))
}

func requireSnapshotGone(t *testing.T, store *testutil.Store, snapshotID string) {
	t.Helper()
	ctx := context.Background()
	_, err := store.Snapshots.Stat(ctx, snapshotID)
	require.True(t, cerrdefs.IsNotFound(err), "snapshot %s still exists: %v", snapshotID, err)
	all, err := store.Leases.List(ctx)
	require.NoError(t, err)
	for _, lease := range all {
		resources, err := store.Leases.ListResources(ctx, lease)
		require.NoError(t, err)
		for _, resource := range resources {
			require.NotEqual(t, snapshotID, resource.ID, "lease %s still references %s", lease.ID, snapshotID)
		}
	}
}

// The reused snapshot must survive its donor's release and GC until the
// result's owner lease attaches, as a freshly created snapshot does.
func TestLeaseExistingSnapshotSurvivesDonorRelease(t *testing.T) {
	store := testutil.NewStore(t)
	parent, parentOwner := store.Build(t, nil, "prefix", "owned prefix")
	child, childOwner := store.Build(t, parent, "suffix", "owned suffix")
	snap := child.SnapshotID()

	opCtx, releaseOp := operationLease(t, store)
	reused, err := store.Manager.LeaseExistingSnapshot(opCtx, snap)
	require.NoError(t, err)
	require.Equal(t, snap, reused.SnapshotID())

	dropOwner(t, store, child, childOwner)
	dropOwner(t, store, parent, parentOwner)
	store.GC(t)

	require.NoError(t, store.Manager.AttachLease(context.Background(), "published-result", snap))
	releaseOp()
	store.GC(t)
	testutil.CheckFile(t, reused, "prefix", "owned prefix")
	testutil.CheckFile(t, reused, "suffix", "owned suffix")

	require.NoError(t, reused.Release(context.Background()))
	require.NoError(t, store.Manager.RemoveLease(context.Background(), "published-result"))
	store.GC(t)
	requireSnapshotGone(t, store, snap)
}

// A reused snapshot whose result is never published is released with the
// operation.
func TestLeaseExistingSnapshotReleasedWithOperation(t *testing.T) {
	store := testutil.NewStore(t)
	donor, donorOwner := store.Build(t, nil, "dagger.json", `{"name":"test"}`)
	snap := donor.SnapshotID()

	opCtx, releaseOp := operationLease(t, store)
	reused, err := store.Manager.LeaseExistingSnapshot(opCtx, snap)
	require.NoError(t, err)
	dropOwner(t, store, donor, donorOwner)
	store.GC(t)
	testutil.CheckFile(t, reused, "dagger.json", `{"name":"test"}`)

	require.NoError(t, reused.Release(context.Background()))
	releaseOp()
	store.GC(t)
	requireSnapshotGone(t, store, snap)
}

// GC between attachment and the existence check cannot collect the snapshot.
func TestLeaseExistingSnapshotGCAfterAttach(t *testing.T) {
	store := testutil.NewStore(t)
	donor, donorOwner := store.Build(t, nil, "dagger.json", `{"name":"test"}`)
	snap := donor.SnapshotID()

	opCtx, releaseOp := operationLease(t, store)
	defer releaseOp()
	store.AfterAdd = func(_ context.Context, _ leases.Lease, resource leases.Resource) {
		if resource.ID != snap {
			return
		}
		dropOwner(t, store, donor, donorOwner)
		store.GC(t)
	}
	reused, err := store.Manager.LeaseExistingSnapshot(opCtx, snap)
	store.AfterAdd = nil
	require.NoError(t, err)
	defer reused.Release(context.Background())
	testutil.CheckFile(t, reused, "dagger.json", `{"name":"test"}`)
}

// A candidate collected before attachment is reported missing. The shared
// operation lease and its other snapshots are left alone, and nothing is
// left behind once the operation ends.
func TestLeaseExistingSnapshotCollectedBeforeAttach(t *testing.T) {
	store := testutil.NewStore(t)
	donor, donorOwner := store.Build(t, nil, "dagger.json", `{"name":"test"}`)
	snap := donor.SnapshotID()
	kept, keptOwner := store.Build(t, nil, "kept", "kept")

	opCtx, releaseOp := operationLease(t, store)
	keptRef, err := store.Manager.LeaseExistingSnapshot(opCtx, kept.SnapshotID())
	require.NoError(t, err)
	defer keptRef.Release(context.Background())
	dropOwner(t, store, kept, keptOwner)

	store.BeforeAdd = func(_ context.Context, _ leases.Lease, resource leases.Resource) error {
		if resource.ID == snap {
			dropOwner(t, store, donor, donorOwner)
			store.GC(t)
		}
		return nil
	}
	_, err = store.Manager.LeaseExistingSnapshot(opCtx, snap)
	store.BeforeAdd = nil
	require.Error(t, err)
	require.Equal(t, snap+": not found", err.Error())

	store.GC(t)
	testutil.CheckFile(t, keptRef, "kept", "kept")

	releaseOp()
	store.GC(t)
	requireSnapshotGone(t, store, snap)
	requireSnapshotGone(t, store, kept.SnapshotID())
}

// Without an operation lease there is nothing to own the snapshot, so the
// method fails instead of returning an unprotected ref.
func TestLeaseExistingSnapshotRequiresLease(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	donor, _ := store.Build(t, nil, "dagger.json", `{"name":"test"}`)
	before, err := store.Leases.List(ctx)
	require.NoError(t, err)

	_, err = store.Manager.LeaseExistingSnapshot(ctx, donor.SnapshotID())
	require.ErrorContains(t, err, "no lease in context")
	after, err := store.Leases.List(ctx)
	require.NoError(t, err)
	require.Len(t, after, len(before))
}
