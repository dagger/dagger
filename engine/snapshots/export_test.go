package snapshots

import (
	"context"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// RecordSnapshotContent records desc's blob as the snapshot's, as an export
// of the snapshot's layer does.
func RecordSnapshotContent(sm SnapshotManager, snapshotID string, desc ocispecs.Descriptor) error {
	return sm.(*snapshotManager).recordSnapshotContent(context.Background(), snapshotID, desc)
}

// OwnerRecordCounts reports how many (snapshot, owner lease) pairs the
// manager's in-memory owner records hold, by snapshot and by lease.
func OwnerRecordCounts(sm SnapshotManager) (bySnapshot, byLease int) {
	cm := sm.(*snapshotManager)
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for _, owners := range cm.snapshotOwnerLeases {
		bySnapshot += len(owners)
	}
	for _, owned := range cm.leaseOwnedSnapshots {
		byLease += len(owned)
	}
	return bySnapshot, byLease
}
