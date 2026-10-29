package snapshots

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// Removing a lease takes it off every snapshot it owns, and leaves the
// snapshots' other owners, and the snapshots it doesn't own, as they were.
func TestRemoveLeaseOwners(t *testing.T) {
	ctx := context.Background()
	cm := newApplySnapshotDiffTestManager(t)
	require.Empty(t, cm.addSnapshotOwner("s1", "a", nil))
	require.Empty(t, cm.addSnapshotOwner("s2", "a", nil))
	require.Empty(t, cm.addSnapshotOwner("s1", "b", nil))
	require.Empty(t, cm.addSnapshotOwner("s3", "b", nil))

	require.NoError(t, cm.RemoveLease(ctx, "a"))
	require.Equal(t, map[string]map[string]struct{}{
		"s1": {"b": {}},
		"s3": {"b": {}},
	}, cm.snapshotOwnerLeases)

	require.NoError(t, cm.RemoveLease(ctx, "b"))
	require.Empty(t, cm.snapshotOwnerLeases)

	// A lease that owned something before owns it again once added back.
	require.Empty(t, cm.addSnapshotOwner("s2", "a", nil))
	require.NoError(t, cm.RemoveLease(ctx, "a"))
	require.Empty(t, cm.snapshotOwnerLeases)
}

// Engines remove owner leases in bulk, as when many results are released at
// once, on engines with tens of thousands of owned snapshots. A removal's
// cost should not grow with the other snapshots' owners.
func BenchmarkRemoveLeaseAmongOwnedSnapshots(b *testing.B) {
	for _, snapshots := range []int{10_000, 100_000} {
		b.Run(fmt.Sprintf("snapshots=%d", snapshots), func(b *testing.B) {
			ctx := context.Background()
			cm := newApplySnapshotDiffTestManager(b)
			for i := range snapshots {
				cm.addSnapshotOwner(fmt.Sprintf("snapshot-%d", i), fmt.Sprintf("owner-%d", i), nil)
			}
			b.ResetTimer()
			for i := range b.N {
				pin := fmt.Sprintf("pin-%d", i)
				cm.addSnapshotOwner(fmt.Sprintf("snapshot-%d", i%snapshots), pin, nil)
				if err := cm.RemoveLease(ctx, pin); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
