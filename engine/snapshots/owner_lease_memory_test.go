package snapshots_test

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"runtime/pprof"
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
// one build lease, as a chain of Directory.withNewFile calls does.
func buildChain(t testing.TB, store *testutil.Store, depth int) []bkcache.ImmutableRef {
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
	t.Cleanup(func() {
		for _, ref := range refs {
			_ = ref.Release(context.Background())
		}
	})
	return refs
}

// resultLeaseID is a dagql result's owner lease ID for its snapshot role, in
// the shape dagql makes them (resultSnapshotLeaseIDForKey).
func resultLeaseID(result int) string {
	return fmt.Sprintf("dagql/result/%d/%s", result, url.PathEscape(`["","snapshot"]`))
}

func leaseResourceCount(t testing.TB, store *testutil.Store, prefix string) int {
	t.Helper()
	ctx := context.Background()
	all, err := store.Leases.List(ctx)
	require.NoError(t, err)
	n := 0
	for _, l := range all {
		if len(l.ID) < len(prefix) || l.ID[:len(prefix)] != prefix {
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
// lease database and in memory, and the live heap they retain.
func TestOwnerLeaseRecordsOnAChain(t *testing.T) {
	for _, depth := range []int{50, 200} {
		t.Run(fmt.Sprintf("depth=%d", depth), func(t *testing.T) {
			ctx := context.Background()
			store := testutil.NewStore(t)
			refs := buildChain(t, store, depth)
			base := liveHeap()
			for i, ref := range refs {
				require.NoError(t, store.Manager.AttachLease(ctx, resultLeaseID(i), ref.SnapshotID()))
			}
			grown := int64(liveHeap()) - int64(base)
			resources := leaseResourceCount(t, store, "dagql/result/")
			bySnapshot, byLease := bkcache.OwnerRecordCounts(store.Manager)
			t.Logf("%d results on a chain of %d: %d lease resources, %d+%d in-memory owner records; live heap +%d B (%d B/result, %d B/resource)",
				depth, depth, resources, bySnapshot, byLease, grown, grown/int64(depth), grown/int64(max(resources, 1)))
			writeOwnerLeaseHeapProfile(t, fmt.Sprintf("chain-%d", depth))
			runtime.KeepAlive(refs)
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
	refs := buildChain(t, store, depth)
	top := refs[len(refs)-1].SnapshotID()
	base := liveHeap()
	for i := range results {
		require.NoError(t, store.Manager.AttachLease(ctx, resultLeaseID(i), top))
	}
	grown := int64(liveHeap()) - int64(base)
	resources := leaseResourceCount(t, store, "dagql/result/")
	bySnapshot, byLease := bkcache.OwnerRecordCounts(store.Manager)
	t.Logf("%d results on one snapshot %d deep: %d lease resources, %d+%d in-memory owner records; live heap +%d B (%d B/result)",
		results, depth, resources, bySnapshot, byLease, grown, grown/int64(results))
	runtime.KeepAlive(refs)
}
