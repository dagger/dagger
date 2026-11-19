package snapshots_test

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"runtime/pprof"
	"slices"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

var ownerLeaseHeapProfile = flag.String("owner-lease-heapprofile", "", "write the owner lease measurements' heap profiles to this path prefix")

// writeOwnerLeaseHeapProfile writes the heap profile as of a full collection
// to the -owner-lease-heapprofile prefix, if set, while the records measured
// are still live.
func writeOwnerLeaseHeapProfile(t testing.TB, name string) {
	t.Helper()
	if *ownerLeaseHeapProfile == "" {
		return
	}
	runtime.GC()
	f, err := os.Create(*ownerLeaseHeapProfile + "-" + name + ".pb.gz")
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, pprof.Lookup("heap").WriteTo(f, 0))
}

// liveHeap is the live heap after a full collection.
func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// buildChain commits depth empty snapshots, each on top of the last, under
// one build lease, as a chain of Directory.withNewFile calls does. The build
// lease is returned for the caller to delete.
func buildChain(t testing.TB, store *testutil.Store, depth int) ([]bkcache.ImmutableRef, leases.Lease) {
	t.Helper()
	ctx := context.Background()
	build, err := store.Leases.Create(ctx, leases.WithRandomID())
	require.NoError(t, err)
	ctx = leases.WithLease(ctx, build.ID)
	refs := make([]bkcache.ImmutableRef, 0, depth)
	var parent bkcache.ImmutableRef
	for range depth {
		mut, err := store.Manager.New(ctx, parent)
		require.NoError(t, err)
		ref, err := mut.CommitWithUsage(ctx, ctdsnapshots.Usage{})
		require.NoError(t, err)
		refs = append(refs, ref)
		parent = ref
	}
	return refs, build
}

// releaseChain releases the refs and deletes the build lease, so that only
// owner leases keep the chain.
func releaseChain(t testing.TB, store *testutil.Store, refs []bkcache.ImmutableRef, build leases.Lease) {
	t.Helper()
	ctx := context.Background()
	for _, ref := range refs {
		require.NoError(t, ref.Release(ctx))
	}
	require.NoError(t, store.Leases.Delete(ctx, build))
}

// resultLeaseID is a dagql result's owner lease ID for its snapshot role, in
// the shape dagql makes them (resultSnapshotLeaseIDForKey).
func resultLeaseID(result int) string {
	return fmt.Sprintf("dagql/result/%d/%s", result, url.PathEscape(`["","snapshot"]`))
}

const ancestryLeasePrefix = "dagger/owner-ancestry/"

// ownerHoldings lists what holds an owner lease's snapshots for the
// collector: the lease's own snapshots and blobs, and, for a dagql owner
// lease, the owner ancestry leases', which hold its ancestors.
func ownerHoldings(t testing.TB, store *testutil.Store, owner string) (snapshots, contents []string) {
	t.Helper()
	ctx := context.Background()
	all, err := store.Leases.List(ctx)
	require.NoError(t, err)
	for _, l := range all {
		if l.ID != owner && !(strings.HasPrefix(owner, "dagql/result/") && strings.HasPrefix(l.ID, ancestryLeasePrefix)) {
			continue
		}
		resources, err := store.Leases.ListResources(ctx, l)
		require.NoError(t, err)
		for _, r := range resources {
			if r.Type == "content" {
				contents = append(contents, r.ID)
			} else {
				snapshots = append(snapshots, r.ID)
			}
		}
	}
	return snapshots, contents
}

// ancestryHoldings lists the owner ancestry leases' snapshots and blobs.
func ancestryHoldings(t testing.TB, store *testutil.Store) (snapshots, contents []string) {
	t.Helper()
	return ownerHoldings(t, store, "dagql/result/\x00none")
}

// leaseResourceCount counts the resources of the leases whose IDs start with
// any of prefixes.
func leaseResourceCount(t testing.TB, store *testutil.Store, prefixes ...string) int {
	t.Helper()
	ctx := context.Background()
	all, err := store.Leases.List(ctx)
	require.NoError(t, err)
	n := 0
	for _, l := range all {
		if !slices.ContainsFunc(prefixes, func(prefix string) bool { return strings.HasPrefix(l.ID, prefix) }) {
			continue
		}
		resources, err := store.Leases.ListResources(ctx, l)
		require.NoError(t, err)
		n += len(resources)
	}
	return n
}

// TestOwnerLeaseRecordsOnAChain measures what owner leases cost when each
// result owns its own snapshot of one chain, as each result of a chain of
// Directory.withNewFile calls does: the records each lease keeps, in the
// lease database and in memory, and the live heap they retain. Each owner
// lease names its own snapshot, and the ancestry lease each ancestor once:
// records grow with the results, not with results times depth.
func TestOwnerLeaseRecordsOnAChain(t *testing.T) {
	for _, depth := range []int{50, 200} {
		t.Run(fmt.Sprintf("depth=%d", depth), func(t *testing.T) {
			ctx := context.Background()
			store := testutil.NewStore(t)
			refs, build := buildChain(t, store, depth)
			base := liveHeap()
			for i, ref := range refs {
				require.NoError(t, store.Manager.AttachLease(ctx, resultLeaseID(i), ref.SnapshotID()))
			}
			grown := int64(liveHeap()) - int64(base)
			resources := leaseResourceCount(t, store, "dagql/result/", ancestryLeasePrefix)
			bySnapshot, byLease := bkcache.OwnerRecordCounts(store.Manager)
			t.Logf("%d results on a chain of %d: %d lease resources, %d+%d in-memory owner records; live heap +%d B (%d B/result)",
				depth, depth, resources, bySnapshot, byLease, grown, grown/int64(depth))
			writeOwnerLeaseHeapProfile(t, fmt.Sprintf("chain-%d", depth))
			require.Equal(t, depth+depth-1, resources, "each owner names its snapshot, the ancestry lease each ancestor")
			require.Equal(t, depth, bySnapshot)
			require.Equal(t, depth, byLease)

			// The owners alone keep the chain; removing them from the top
			// down frees it one snapshot at a time.
			releaseChain(t, store, refs, build)
			for top := depth - 1; top >= 0; top-- {
				if top%25 == 0 || top == depth-1 {
					store.GC(t)
					for j, ref := range refs {
						require.Equal(t, j <= top, snapshotPresent(t, store, ref.SnapshotID()), "snapshot %d with owners up to %d", j, top)
					}
				}
				require.NoError(t, store.Manager.RemoveLease(ctx, resultLeaseID(top)))
			}
			store.GC(t)
			require.False(t, snapshotPresent(t, store, refs[0].SnapshotID()))
			require.Zero(t, leaseResourceCount(t, store, "dagql/result/", ancestryLeasePrefix))
		})
	}
}

// TestOwnerLeaseRecordsOnASharedSnapshot measures the same when many results
// own one snapshot at the top of a chain, as results whose values share a
// snapshot do.
func TestOwnerLeaseRecordsOnASharedSnapshot(t *testing.T) {
	const depth, results = 50, 200
	ctx := context.Background()
	store := testutil.NewStore(t)
	refs, build := buildChain(t, store, depth)
	top := refs[len(refs)-1].SnapshotID()
	base := liveHeap()
	for i := range results {
		require.NoError(t, store.Manager.AttachLease(ctx, resultLeaseID(i), top))
	}
	grown := int64(liveHeap()) - int64(base)
	resources := leaseResourceCount(t, store, "dagql/result/", ancestryLeasePrefix)
	bySnapshot, byLease := bkcache.OwnerRecordCounts(store.Manager)
	t.Logf("%d results on one snapshot %d deep: %d lease resources, %d+%d in-memory owner records; live heap +%d B (%d B/result)",
		results, depth, resources, bySnapshot, byLease, grown, grown/int64(results))
	require.Equal(t, results+depth-1, resources)
	require.Equal(t, results, bySnapshot)

	// The chain stays while any one owner does, and goes with the last.
	releaseChain(t, store, refs, build)
	for i := range results - 1 {
		require.NoError(t, store.Manager.RemoveLease(ctx, resultLeaseID(i)))
	}
	store.GC(t)
	for _, ref := range refs {
		require.True(t, snapshotPresent(t, store, ref.SnapshotID()), "the last owner keeps the chain")
	}
	require.Equal(t, 1+depth-1, leaseResourceCount(t, store, "dagql/result/", ancestryLeasePrefix))
	require.NoError(t, store.Manager.RemoveLease(ctx, resultLeaseID(results-1)))
	store.GC(t)
	for _, ref := range refs {
		require.False(t, snapshotPresent(t, store, ref.SnapshotID()))
	}
	require.Zero(t, leaseResourceCount(t, store, "dagql/result/", ancestryLeasePrefix))
}
