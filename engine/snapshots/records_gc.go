package snapshots

import (
	"context"
	stderrors "errors"

	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	pkgerrors "github.com/pkg/errors"
)

// pruneStaleRecordsBatch bounds how many records are dropped under a single
// hold of cm.mu, so that a large sweep cannot stall the manager.
const pruneStaleRecordsBatch = 256

// PruneStaleRecords drops in-memory records and metadata whose snapshot no
// longer exists in the snapshotter, and reports how many it dropped.
//
// Snapshot metadata is in-memory only, and the one place it is released is
// cacheRecord.remove, reached when a mutable ref is committed or released.
// Committed records are evicted from disk by containerd's lease garbage
// collector instead, which cannot reach back into these maps, so their
// metadata stays reachable for the lifetime of the process.
//
// That metadata is not small. contenthash marshals the path tree of an entire
// snapshot under a single key, so an engine accumulates one such tree for
// every distinct directory it has ever checksummed rather than for every one
// it still holds on disk.
func (cm *snapshotManager) PruneStaleRecords(ctx context.Context) (int, error) {
	// Collect candidates before walking, not after: the walk observes the
	// snapshotter at some point after this returns, so anything committed
	// here already existed by then. A candidate missing from the walk was
	// therefore deleted rather than not yet created, and snapshot IDs are
	// never reused, so it is gone for good.
	candidates := cm.staleRecordCandidates()
	if len(candidates) == 0 {
		return 0, nil
	}

	live, err := cm.liveSnapshotIDs(ctx)
	if err != nil {
		return 0, err
	}

	stale := make([]string, 0, len(candidates))
	for id, snapshotID := range candidates {
		if _, ok := live[snapshotID]; !ok {
			stale = append(stale, id)
		}
	}

	var (
		removed int
		rerr    error
	)
	for len(stale) > 0 {
		batch := stale
		if len(batch) > pruneStaleRecordsBatch {
			batch = batch[:pruneStaleRecordsBatch]
		}
		stale = stale[len(batch):]

		n, err := cm.removeStaleRecords(ctx, batch)
		removed += n
		rerr = stderrors.Join(rerr, err)
	}

	if removed > 0 {
		bklog.G(ctx).Debugf("pruned %d stale snapshot records", removed)
	}
	return removed, rerr
}

// staleRecordCandidates returns id -> snapshot ID for every record and every
// recordless metadata entry that is eligible to be dropped.
//
// Eligibility is committed metadata that is not held by a mutable or locked
// record. Every path that creates a record creates its snapshot first and
// marks the metadata committed afterwards, so a committed entry is one whose
// snapshot has certainly been written. Uncommitted and mutable entries may be
// mid-creation and are left alone.
func (cm *snapshotManager) staleRecordCandidates() map[string]string {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidates := make(map[string]string)
	for id, rec := range cm.records {
		if rec.mutable || rec.locked {
			continue
		}
		if !rec.md.getCommitted() {
			continue
		}
		candidates[id] = rec.md.getSnapshotID()
	}

	for _, id := range cm.metadataStore.ids() {
		if _, ok := candidates[id]; ok {
			continue
		}
		if _, ok := cm.records[id]; ok {
			// Held by a record that was skipped above.
			continue
		}
		md, ok := cm.metadataStore.get(id)
		if !ok || !md.getCommitted() {
			continue
		}
		candidates[id] = md.getSnapshotID()
	}
	return candidates
}

// removeStaleRecords drops one batch of ids, re-checking eligibility under the
// lock in case the entry became live again while the batch was queued.
func (cm *snapshotManager) removeStaleRecords(ctx context.Context, ids []string) (int, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	var (
		removed int
		rerr    error
	)
	for _, id := range ids {
		rec, ok := cm.records[id]
		if !ok {
			// Recordless metadata: dropping the entry is all there is to do.
			if md, ok := cm.metadataStore.get(id); ok && md.getCommitted() {
				cm.metadataStore.clear(id)
				removed++
			}
			continue
		}
		if rec.mutable || rec.locked || !rec.md.getCommitted() {
			continue
		}
		if err := rec.remove(ctx); err != nil {
			rerr = stderrors.Join(rerr, pkgerrors.Wrapf(err, "remove stale record %s", id))
			continue
		}
		removed++
	}
	return removed, rerr
}

// liveSnapshotIDs returns the names of every snapshot the snapshotter still
// holds. The walk runs without cm.mu held: it reads the snapshotter's own
// store, which can be slow, and PruneStaleRecords is written so that a stale
// result can only ever cause a sweep to skip an entry, never to drop a live
// one.
func (cm *snapshotManager) liveSnapshotIDs(ctx context.Context) (map[string]struct{}, error) {
	live := make(map[string]struct{})
	if err := cm.Snapshotter.Walk(ctx, func(_ context.Context, info ctdsnapshots.Info) error {
		live[info.Name] = struct{}{}
		return nil
	}); err != nil {
		return nil, pkgerrors.Wrap(err, "walk snapshots")
	}
	return live, nil
}
