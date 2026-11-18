package snapshots

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
