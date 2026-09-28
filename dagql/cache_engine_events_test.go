package dagql

// What the cache reports for the engine's cache events: each retention edge
// a prune run drops, with the time of its own drop; the entries a boot
// restore installed, type definitions aside; and the parts a snapshot-sharing
// pass completed.

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// A prune run drops its edges one by one and releases the graph lock between
// them, so a persistable hit can create an edge again in between. Each drop
// is timed where its edge is deleted: the re-creation between two drops falls
// between their times, and the next run drops the re-created edge again.
func TestCachePruneReportsEachDrop(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })

	// Three retained entries, which an unpruneable root keeps after their
	// own edges go.
	rootFrame := cacheTestIntCall("prune-drop-root")
	root, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, &CallRequest{ResultCall: rootFrame}, ValueFunc(cacheTestIntResult(rootFrame, 0)))
	assert.NilError(t, err)
	assert.NilError(t, c.MakeResultUnpruneable(ctx, root))
	var frames []*ResultCall
	ids := map[sharedResultID]*ResultCall{}
	for _, field := range []string{"prune-drop-a", "prune-drop-b", "prune-drop-c"} {
		frame := cacheTestIntCall(field)
		res, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, ValueFunc(cacheTestIntResult(frame, 1)))
		assert.NilError(t, err)
		assert.NilError(t, c.AddExplicitDependency(ctx, root, res, "test"))
		frames = append(frames, frame)
		ids[res.cacheSharedResult().id] = frame
	}
	assert.NilError(t, c.ReleaseSession(ctx, "s"))

	// After the run's first drop, another session hits the dropped entry and
	// creates its edge again.
	var (
		mu         sync.Mutex
		recreated  time.Time
		recreateID sharedResultID
	)
	c.testAfterRetentionDrop = func(id sharedResultID) {
		mu.Lock()
		defer mu.Unlock()
		if !recreated.IsZero() {
			return
		}
		_, err := c.GetOrInitCall(ctx, "hitter", noopTypeResolver{}, &CallRequest{ResultCall: ids[id].clone(), IsPersistable: true}, func(context.Context) (AnyResult, error) {
			t.Error("a hit runs no resolver")
			return nil, nil
		})
		assert.NilError(t, err)
		recreated, recreateID = time.Now(), id
	}
	report, err := c.Prune(ctx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, len(frames), len(report.DroppedEdges))
	assert.Equal(t, uint64(recreateID), report.DroppedEdges[0].ResultID)
	for i, drop := range report.DroppedEdges {
		_, ok := ids[sharedResultID(drop.ResultID)]
		assert.Assert(t, ok, "drop %d names a pruned entry", i)
		if i > 0 {
			assert.Assert(t, report.DroppedEdges[i-1].DroppedAt.Before(drop.DroppedAt), "drops are timed one by one")
		}
	}
	assert.Assert(t, report.DroppedEdges[0].DroppedAt.Before(recreated), "the first drop precedes the re-creation")
	assert.Assert(t, recreated.Before(report.DroppedEdges[1].DroppedAt), "which precedes the next drop")

	// The re-created edge is dropped by the next run, at its own time, once
	// the hitting session has ended.
	c.testAfterRetentionDrop = nil
	assert.NilError(t, c.ReleaseSession(ctx, "hitter"))
	again, err := c.Prune(ctx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(again.DroppedEdges))
	assert.Equal(t, uint64(recreateID), again.DroppedEdges[0].ResultID)
	assert.Assert(t, recreated.Before(again.DroppedEdges[0].DroppedAt))

	// A run with nothing to drop reports none.
	empty, err := c.Prune(ctx, []CachePrunePolicy{{All: true}})
	assert.NilError(t, err)
	assert.Equal(t, 0, len(empty.DroppedEdges))
}

// A metadata prune reports the edges it drops the same way.
func TestCacheMetadataPruneReportsDrops(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })
	frame := cacheTestIntCall("metadata-prune-drop")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, ValueFunc(cacheTestIntResult(frame, 1)))
	assert.NilError(t, err)
	id := res.cacheSharedResult().id
	cacheTestReleaseSession(t, c, ctx)

	before := time.Now()
	report, err := c.PruneMetadataEstimate(ctx, c.MetadataEstimate().EstimatedBytes-1, 1)
	assert.NilError(t, err)
	assert.Equal(t, 1, report.RemovedPersistedRootCount)
	assert.Equal(t, 1, len(report.DroppedEdges))
	assert.Equal(t, uint64(id), report.DroppedEdges[0].ResultID)
	assert.Assert(t, !report.DroppedEdges[0].DroppedAt.Before(before))
}

// The restored count leaves out type definitions: entries whose call is
// profile-skipped.
func TestCacheBootRestoredResultsExcludesTypeDefinitions(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	path := filepath.Join(t.TempDir(), "cache.db")
	c, err := NewCache(ctx, path, nil, nil)
	assert.NilError(t, err)
	for i, skip := range []bool{false, true, false, true, true} {
		frame := cacheTestIntCall("restored-count-" + string(rune('a'+i)))
		frame.ProfileSkip = skip
		_, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, ValueFunc(cacheTestIntResult(frame, i)))
		assert.NilError(t, err)
	}
	assert.NilError(t, c.ReleaseSession(ctx, "s"))
	assert.Equal(t, 0, c.BootRestoredResults(), "a new cache restored nothing")
	assert.NilError(t, c.Close(context.Background()))
	assert.Equal(t, 5, c.PersistedResults())

	c, err = NewCache(ctx, path, nil, nil)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, c.Close(context.Background())) })
	assert.Equal(t, 5, c.Size(), "every entry is restored")
	assert.Equal(t, 2, c.BootRestoredResults(), "type definitions aside")
}

// A snapshot-sharing pass reports the parts it completed, with each entry's
// number and dependencies, as they stand once the part is installed: the
// donor's part brings a dependency the receiver did not have.
func TestSnapshotShareReportsCompletedParts(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	var (
		mu      sync.Mutex
		reports [][]SnapshotSharedPart
	)
	c.shareReport = func(parts []SnapshotSharedPart) {
		mu.Lock()
		defer mu.Unlock()
		reports = append(reports, slices.Clone(parts))
	}
	barrier := newSharePassBarrier(c)
	partDep := persistedListTestResult(t, ctx, c, srv, "report-part-dep", &transferTestValue{Text: "dependency"})
	partDepID := uint64(partDep.cacheSharedResult().id)
	donor := persistedListTestResult(t, ctx, c, srv, "report-donor", &transferTestValue{Text: "snapshot", PartDeps: []uint64{partDepID}, links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "donor-snapshot"}}})
	receiver := persistedListTestResult(t, ctx, c, srv, "report-receiver", &transferTestValue{Text: "pending"})
	c.egraphMu.RLock()
	_, hadDep := receiver.cacheSharedResult().deps[sharedResultID(partDepID)]
	c.egraphMu.RUnlock()
	assert.Assert(t, !hadDep, "the receiver has no dependency on it before the share")
	shareTestEncodedReceiver(t, ctx, c, receiver)
	partTestEquivalent(t, c, receiver, donor)
	shareTestUnite(t, ctx, c, "share-report", donor, receiver)
	assert.Equal(t, 1, barrier.awaitPass(t), "one planned slot")

	snapshotKey, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	assert.NilError(t, err)
	c.egraphMu.RLock()
	deps := sortedResultIDs(receiver.cacheSharedResult().deps)
	c.egraphMu.RUnlock()
	assert.Assert(t, slices.Contains(deps, partDepID), "the installed part brought its dependency: %v", deps)
	mu.Lock()
	defer mu.Unlock()
	assert.DeepEqual(t, [][]SnapshotSharedPart{{{
		ResultID: uint64(receiver.cacheSharedResult().id),
		Part:     snapshotKey,
		Deps:     deps,
	}}}, reports)
}
