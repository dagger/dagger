package snapshots

import "os"

// MetadataMemoryStats is what a manager's in-memory snapshot metadata holds,
// and the content-hash trees it keeps on disk.
type MetadataMemoryStats struct {
	// Records counts cache records.
	Records int
	// Metadata counts metadata entries, with or without a record.
	Metadata int
	// PendingContentHashes counts loaded hash records not yet opened.
	PendingContentHashes int
	// ExternalBytes sums the external values of the metadata entries, the
	// pending hash records and the trees that could not be written to disk:
	// the serialized content-hash trees held in memory.
	ExternalBytes int
	// ContentHashFiles counts the content-hash tree files on disk, those
	// still to delete included, and ContentHashFileBytes sums their sizes.
	ContentHashFiles     int
	ContentHashFileBytes int
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
	if files := s.hashFiles; files != nil {
		files.mu.Lock()
		defer files.mu.Unlock()
		for _, ids := range []map[string]struct{}{files.ids, files.orphans} {
			for id := range ids {
				stats.ContentHashFiles++
				if fi, err := os.Stat(files.path(id)); err == nil {
					stats.ContentHashFileBytes += int(fi.Size())
				}
			}
		}
		for _, dt := range files.mem {
			stats.ExternalBytes += len(dt)
		}
	}
	return stats
}

// ContentHashBytes is the bytes of content-hash trees the stats count, in
// memory and on disk.
func (s MetadataMemoryStats) ContentHashBytes() int {
	return s.ExternalBytes + s.ContentHashFileBytes
}
