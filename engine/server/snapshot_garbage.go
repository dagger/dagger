package server

import (
	"context"
	"fmt"
	"sync/atomic"

	ctdmetadata "github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/pkg/gc"

	"github.com/dagger/dagger/engine/slog"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// snapshotGarbage collects snapshot and content data that nothing references
// anymore.
//
// Removing the last lease on a snapshot only marks containerd's metadata DB
// dirty; the data stays on disk until GarbageCollect runs. Leases are removed
// all the time without a prune (session teardown, failed execs, stopped
// services), so pruning alone is not a sufficient trigger: an engine whose
// policies never remove an entry would never reclaim that data. Instead the
// engine records pending deletions here and collects them on its regular GC
// passes.
type snapshotGarbage struct {
	db      *ctdmetadata.DB
	pending atomic.Bool
	// forget drops the snapshot manager's in-memory metadata of the
	// snapshots a collection removed; nil when it keeps none.
	forget func(context.Context) error
	// forgetPending is set when forget must run after the next collection
	// even if it removes no snapshot: at first, since snapshots may have been
	// removed before the manager was made, and after forget failed.
	forgetPending atomic.Bool
}

func newSnapshotGarbage(db *ctdmetadata.DB, sm bkcache.SnapshotManager) *snapshotGarbage {
	g := &snapshotGarbage{db: db}
	if f, ok := sm.(bkcache.CollectedSnapshotForgetter); ok {
		g.forget = f.ForgetCollectedSnapshots
	}
	// containerd counts deletions in memory only, so garbage left by a
	// previous engine process is invisible to it. Collect once regardless.
	g.pending.Store(true)
	g.forgetPending.Store(true)
	db.RegisterMutationCallback(func(dirty bool) {
		// This runs on the DB's write path while it holds the lock that
		// GarbageCollect needs, so only record the signal here.
		if dirty {
			g.pending.Store(true)
		}
	})
	return g
}

// Collect runs containerd garbage collection unconditionally, then drops the
// in-memory metadata of the snapshots it removed.
func (g *snapshotGarbage) Collect(ctx context.Context) error {
	// Clear before collecting: a deletion that lands during the collection
	// sets the flag again and is picked up by the next pass.
	g.pending.Store(false)
	stats, err := g.db.GarbageCollect(ctx)
	if err != nil {
		g.pending.Store(true)
		return err
	}
	slog.Debug("collected snapshot garbage", "stats", stats)
	// Forgetting walks every snapshot, so it runs only when the collection
	// removed some.
	if g.forget != nil && (removedSnapshots(stats) || g.forgetPending.Load()) {
		g.forgetPending.Store(false)
		if err := g.forget(ctx); err != nil {
			g.forgetPending.Store(true)
			return fmt.Errorf("forget collected snapshots: %w", err)
		}
	}
	return nil
}

// removedSnapshots reports whether a collection with stats removed any
// snapshot: containerd times the cleanup of each snapshotter it removed
// snapshots from, and only those.
func removedSnapshots(stats gc.Stats) bool {
	s, ok := stats.(ctdmetadata.GCStats)
	return !ok || len(s.SnapshotD) > 0
}

// CollectIfPending runs garbage collection if anything was deleted since the
// last collection.
func (g *snapshotGarbage) CollectIfPending(ctx context.Context) error {
	if g == nil || !g.pending.Load() {
		return nil
	}
	return g.Collect(ctx)
}
