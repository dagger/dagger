package snapshots

import digest "github.com/opencontainers/go-digest"

// FetchHolders returns how many imports hold m's download of blob in
// progress, or 0 when none runs.
func FetchHolders(m SnapshotManager, blob digest.Digest) int {
	f := &m.(*snapshotManager).fetches
	f.mu.Lock()
	defer f.mu.Unlock()
	if download := f.inflight[blob]; download != nil {
		return download.refs
	}
	return 0
}
