package snapshots

import (
	"context"
	"fmt"
	"sync"
	"testing"

	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/stretchr/testify/require"
)

// assertOwnerLeaseIndexConsistent checks the invariant between the forward
// and inverse owner-lease indexes:
//
//	leaseID ∈ snapshotOwnerLeases[s] ⟺ s ∈ ownerLeaseSnapshots[leaseID]
func assertOwnerLeaseIndexConsistent(t *testing.T, cm *snapshotManager) {
	t.Helper()
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for snapshotID, leaseIDs := range cm.snapshotOwnerLeases {
		require.NotEmpty(t, leaseIDs, "forward entry with no leases: %s", snapshotID)
		for leaseID := range leaseIDs {
			require.Contains(t, cm.ownerLeaseSnapshots[leaseID], snapshotID,
				"forward without inverse: lease %s owns %s", leaseID, snapshotID)
		}
	}
	for leaseID, snapshotIDs := range cm.ownerLeaseSnapshots {
		require.NotEmpty(t, snapshotIDs, "inverse entry with no snapshots: %s", leaseID)
		for snapshotID := range snapshotIDs {
			require.Contains(t, cm.snapshotOwnerLeases[snapshotID], leaseID,
				"inverse without forward: lease %s owns %s", leaseID, snapshotID)
		}
	}
}

func TestSnapshotManagerOwnerLeaseIndexConsistency(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)
	sn := cm.Snapshotter.(*applySnapshotDiffTestSnapshotter)
	sn.snapshots["base"] = ctdsnapshots.Info{Name: "base"}
	sn.snapshots["mid"] = ctdsnapshots.Info{Name: "mid", Parent: "base"}
	sn.snapshots["top"] = ctdsnapshots.Info{Name: "top", Parent: "mid"}
	sn.snapshots["other"] = ctdsnapshots.Info{Name: "other"}

	ctx := context.Background()
	require.NoError(t, cm.AttachLease(ctx, "lease-a", "top"))
	require.NoError(t, cm.AttachLease(ctx, "lease-b", "mid"))
	require.NoError(t, cm.AttachLease(ctx, "lease-c", "other"))
	assertOwnerLeaseIndexConsistent(t, cm)

	// A lease owns its attach point and every ancestor of it.
	require.Equal(t, map[string]struct{}{"lease-a": {}}, cm.snapshotOwnerLeases["top"])
	require.Equal(t, map[string]struct{}{"lease-a": {}, "lease-b": {}}, cm.snapshotOwnerLeases["mid"])
	require.Equal(t, map[string]struct{}{"lease-a": {}, "lease-b": {}}, cm.snapshotOwnerLeases["base"])
	require.Equal(t, map[string]struct{}{"lease-c": {}}, cm.snapshotOwnerLeases["other"])

	// Removing one lease must not disturb leases that share the chain.
	require.NoError(t, cm.RemoveLease(ctx, "lease-a"))
	assertOwnerLeaseIndexConsistent(t, cm)
	require.NotContains(t, cm.snapshotOwnerLeases, "top")
	require.Equal(t, map[string]struct{}{"lease-b": {}}, cm.snapshotOwnerLeases["mid"])
	require.Equal(t, map[string]struct{}{"lease-b": {}}, cm.snapshotOwnerLeases["base"])
	require.Equal(t, map[string]struct{}{"lease-c": {}}, cm.snapshotOwnerLeases["other"])

	require.NoError(t, cm.RemoveLease(ctx, "lease-b"))
	require.NoError(t, cm.RemoveLease(ctx, "lease-c"))
	assertOwnerLeaseIndexConsistent(t, cm)
	require.Empty(t, cm.snapshotOwnerLeases)
	require.Empty(t, cm.ownerLeaseSnapshots)

	// Removing a lease that owns nothing is a no-op.
	require.NoError(t, cm.RemoveLease(ctx, "lease-b"))
	require.NoError(t, cm.RemoveLease(ctx, "never-attached"))
	assertOwnerLeaseIndexConsistent(t, cm)
}

func TestSnapshotManagerOwnerLeaseConcurrentAttachRemove(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)
	sn := cm.Snapshotter.(*applySnapshotDiffTestSnapshotter)

	const chains = 4
	const depth = 8
	for c := 0; c < chains; c++ {
		parent := ""
		for d := 0; d < depth; d++ {
			id := fmt.Sprintf("chain%d-%d", c, d)
			sn.snapshots[id] = ctdsnapshots.Info{Name: id, Parent: parent}
			parent = id
		}
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tip := fmt.Sprintf("chain%d-%d", w%chains, depth-1)
			for i := 0; i < 50; i++ {
				leaseID := fmt.Sprintf("lease-%d-%d", w, i)
				if err := cm.AttachLease(ctx, leaseID, tip); err != nil {
					t.Errorf("attach %s: %v", leaseID, err)
					return
				}
				if i%2 == 0 {
					if err := cm.RemoveLease(ctx, leaseID); err != nil {
						t.Errorf("remove %s: %v", leaseID, err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()

	assertOwnerLeaseIndexConsistent(t, cm)

	// The odd-numbered leases survive: 25 per worker, each owning its chain.
	require.Len(t, cm.ownerLeaseSnapshots, 8*25)
	require.Len(t, cm.snapshotOwnerLeases, chains*depth)
	for _, leaseIDs := range cm.snapshotOwnerLeases {
		// Two workers share each chain.
		require.Len(t, leaseIDs, 2*25)
	}
}
