package snapshots

// MetadataMemoryStats is what a manager's in-memory snapshot metadata holds.
type MetadataMemoryStats struct {
	// Records counts cache records.
	Records int
	// Metadata counts metadata entries, with or without a record.
	Metadata int
	// PendingContentHashes counts loaded hash records not yet opened.
	PendingContentHashes int
	// ExternalBytes sums the external values of the metadata entries and
	// the pending hash records: the serialized content-hash trees.
	ExternalBytes int
}

// MetadataMemory reports what m's in-memory snapshot metadata holds.
func MetadataMemory(m SnapshotManager) MetadataMemoryStats {
	cm := m.(*snapshotManager)
	cm.mu.Lock()
	defer cm.mu.Unlock()
	s := cm.metadataStore
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := MetadataMemoryStats{
		Records:              len(cm.records),
		Metadata:             len(s.refs),
		PendingContentHashes: len(s.pendingContentHashes),
	}
	for _, md := range s.refs {
		for _, dt := range md.external {
			stats.ExternalBytes += len(dt)
		}
	}
	for _, dt := range s.pendingContentHashes {
		stats.ExternalBytes += len(dt)
	}
	return stats
}
