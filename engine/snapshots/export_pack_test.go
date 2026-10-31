package snapshots

import (
	"time"

	digest "github.com/opencontainers/go-digest"
)

// PackWaiters returns how many imports hold m's read of the pack id in
// progress, or 0 when none runs.
func PackWaiters(m SnapshotManager, id digest.Digest) int {
	ps := &m.(*snapshotManager).packs
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if f := ps.inflight[id]; f != nil {
		return f.refs
	}
	return 0
}

// PackLeaseTTL is how long a pack read's lease lasts, and PackReads how
// many packs are read at once.
const (
	PackLeaseTTL = packLeaseTTL
	PackReads    = packReads
)

// SetPackReadLimit bounds pack reads to limit until the returned restore.
func SetPackReadLimit(limit time.Duration) (restore func()) {
	old := packReadLimit
	packReadLimit = limit
	return func() { packReadLimit = old }
}
