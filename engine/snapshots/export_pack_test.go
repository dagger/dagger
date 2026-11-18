package snapshots

import (
	"context"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// DownloadChainContent is m's download of desc's blob from provider, as a
// chain's fetch runs it once it has found the blob missing.
func DownloadChainContent(ctx context.Context, m SnapshotManager, desc ocispecs.Descriptor, provider content.Provider) error {
	_, err := m.(*snapshotManager).downloadChainContent(ctx, desc, provider)
	return err
}

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

// LockManager takes m's own lock, as another holder of it would, and
// returns the function that releases it.
func LockManager(m SnapshotManager) (unlock func()) {
	cm := m.(*snapshotManager)
	cm.mu.Lock()
	return cm.mu.Unlock
}

// SetBeforeJoinPackFetch runs hook as each download of a packed blob looks
// for a read of its pack to join, until the returned restore.
func SetBeforeJoinPackFetch(hook func()) (restore func()) {
	old := beforeJoinPackFetch
	beforeJoinPackFetch = hook
	return func() { beforeJoinPackFetch = old }
}

// SetPackReadLimit bounds pack reads to limit until the returned restore.
func SetPackReadLimit(limit time.Duration) (restore func()) {
	old := packReadLimit
	packReadLimit = limit
	return func() { packReadLimit = old }
}
