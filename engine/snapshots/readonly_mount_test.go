package snapshots_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// viewLeases returns the leases that hold a read-only mount's view snapshot.
func viewLeases(t *testing.T, store *testutil.Store) []string {
	t.Helper()
	ctx := context.Background()
	all, err := store.Leases.List(ctx)
	require.NoError(t, err)
	var ids []string
	for _, l := range all {
		resources, err := store.Leases.ListResources(ctx, l)
		require.NoError(t, err)
		for _, r := range resources {
			if strings.HasSuffix(r.ID, "-view") {
				ids = append(ids, l.ID)
				break
			}
		}
	}
	return ids
}

func leaseIDs(t *testing.T, store *testutil.Store) []string {
	t.Helper()
	all, err := store.Leases.List(context.Background())
	require.NoError(t, err)
	ids := make([]string, 0, len(all))
	for _, l := range all {
		ids = append(ids, l.ID)
	}
	slices.Sort(ids)
	return ids
}

// A read-only mount writes its view lease and the lease's view snapshot
// resource in one metadata transaction; the view itself takes containerd's
// own two. Releasing the mount deletes the lease.
func TestReadOnlyMountWritesViewLeaseInOneTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	ref, owner := buildSnapshot(t, store, nil, "a.txt", "a")

	var commits atomic.Int64
	store.DB.RegisterMutationCallback(func(bool) { commits.Add(1) })
	mounted, err := ref.Mount(ctx, true)
	require.NoError(t, err)
	require.EqualValues(t, 3, commits.Load(), "one commit for the view lease and its resource, two for the view")
	require.Len(t, viewLeases(t, store), 1)

	mounts, release, err := mounted.Mount()
	require.NoError(t, err)
	require.NotEmpty(t, mounts)
	require.NoError(t, release())
	require.Empty(t, viewLeases(t, store), "releasing the mount deletes its view lease")

	testutil.CheckFile(t, ref, "a.txt", "a")
	require.NoError(t, ref.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
}

// A failed view lease write leaves no view lease: the lease and its resource
// roll back together, whether the resource write or the commit fails.
func TestReadOnlyMountViewLeaseWriteFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	ref, owner := buildSnapshot(t, store, nil, "a.txt", "a")
	injected := errors.New("injected view lease failure")
	before := leaseIDs(t, store)

	store.BeforeAdd = func(_ context.Context, _ leases.Lease, r leases.Resource) error {
		if strings.HasSuffix(r.ID, "-view") {
			return injected
		}
		return nil
	}
	_, err := ref.Mount(ctx, true)
	store.BeforeAdd = nil
	require.ErrorIs(t, err, injected)
	require.Equal(t, before, leaseIDs(t, store), "the rolled-back view lease is not left behind")

	store.LeaseCommitErr = injected
	_, err = ref.Mount(ctx, true)
	store.LeaseCommitErr = nil
	require.ErrorIs(t, err, injected)
	require.Equal(t, before, leaseIDs(t, store), "the rolled-back view lease is not left behind")

	testutil.CheckFile(t, ref, "a.txt", "a")
	require.Empty(t, viewLeases(t, store))
	require.NoError(t, ref.Release(ctx))
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
}
