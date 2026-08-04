package dagql

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCacheMetadataEstimateFormula(t *testing.T) {
	t.Parallel()

	c := &Cache{}
	assert.DeepEqual(t, c.cacheMetadataEstimateLocked(), CacheMetadataEstimate{})

	c.resultsByID = map[sharedResultID]*sharedResult{
		1: {id: 1},
		2: {id: 2},
	}
	c.egraphTerms = map[egraphTermID]*egraphTerm{
		1: {},
		2: {},
		3: {},
	}
	c.egraphParents = make([]eqClassID, 6)

	assert.DeepEqual(t, c.cacheMetadataEstimateLocked(), CacheMetadataEstimate{
		ResultCount:    2,
		TermCount:      3,
		ClassSlotCount: 5,
		EstimatedBytes: 2*cacheMetadataResultEstimatedBytes +
			3*cacheMetadataTermEstimatedBytes +
			5*cacheMetadataClassSlotEstimatedBytes,
	})

	base := CacheMetadataEstimate{ResultCount: 2, TermCount: 3, ClassSlotCount: 5}
	baseBytes := cacheMetadataResultEstimatedBytes*2 + cacheMetadataTermEstimatedBytes*3 + cacheMetadataClassSlotEstimatedBytes*5
	assert.Equal(t, baseBytes+cacheMetadataResultEstimatedBytes, metadataEstimateWithDelta(base, 1, 0, 0).EstimatedBytes)
	assert.Equal(t, baseBytes+cacheMetadataTermEstimatedBytes, metadataEstimateWithDelta(base, 0, 1, 0).EstimatedBytes)
	assert.Equal(t, baseBytes+cacheMetadataClassSlotEstimatedBytes, metadataEstimateWithDelta(base, 0, 0, 1).EstimatedBytes)
}

func metadataEstimateWithDelta(base CacheMetadataEstimate, results, terms, classes int) CacheMetadataEstimate {
	base.ResultCount += results
	base.TermCount += terms
	base.ClassSlotCount += classes
	base.EstimatedBytes = cacheMetadataResultEstimatedBytes*int64(base.ResultCount) +
		cacheMetadataTermEstimatedBytes*int64(base.TermCount) +
		cacheMetadataClassSlotEstimatedBytes*int64(base.ClassSlotCount)
	return base
}

func TestCacheMetadataDirectResultBytesUsesCeiling(t *testing.T) {
	t.Parallel()

	estimate := CacheMetadataEstimate{ResultCount: 3, TermCount: 2, ClassSlotCount: 1}
	shared := cacheMetadataTermEstimatedBytes*2 + cacheMetadataClassSlotEstimatedBytes
	want := cacheMetadataResultEstimatedBytes + (shared+2)/3
	assert.Equal(t, want, metadataDirectResultBytes(estimate))
	assert.Equal(t, int64(0), metadataDirectResultBytes(CacheMetadataEstimate{}))
}

func TestCacheMetadataPruneCandidateOrder(t *testing.T) {
	t.Parallel()

	now := time.Now()
	snapshot := pruneSnapshot{results: map[sharedResultID]pruneSnapshotResult{
		1: {
			resultID:         1,
			hasPersistedEdge: true,
			entry: CacheUsageEntry{
				CreatedTimeUnixNano:       now.Add(-4 * time.Hour).UnixNano(),
				MostRecentUseTimeUnixNano: now.Add(-3 * time.Hour).UnixNano(),
			},
		},
		2: {
			resultID:         2,
			hasPersistedEdge: true,
			expiresAtUnix:    now.Add(-time.Minute).Unix(),
			entry: CacheUsageEntry{
				CreatedTimeUnixNano:       now.Add(-time.Hour).UnixNano(),
				MostRecentUseTimeUnixNano: now.UnixNano(),
			},
		},
		3: {
			resultID:         3,
			hasPersistedEdge: true,
			entry: CacheUsageEntry{
				CreatedTimeUnixNano:       now.Add(-3 * time.Hour).UnixNano(),
				MostRecentUseTimeUnixNano: now.Add(-2 * time.Hour).UnixNano(),
			},
		},
		4: {
			resultID:         4,
			hasPersistedEdge: true,
			entry: CacheUsageEntry{
				CreatedTimeUnixNano:       now.Add(-2 * time.Hour).UnixNano(),
				MostRecentUseTimeUnixNano: now.Add(-2 * time.Hour).UnixNano(),
			},
		},
	}}

	candidates := (&Cache{}).collectPruneCandidates(
		withMetadataPruneContext(t.Context()),
		-1,
		snapshot,
		nil,
		CachePrunePolicy{All: true},
		now,
	)
	ids := make([]sharedResultID, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.resultID)
	}
	assert.DeepEqual(t, ids, []sharedResultID{2, 1, 3, 4})
}

func TestCachePruneMetadataEstimateSkipsPhysicalMeasurementAndUsesColdOrder(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	var snapshotGCCalls atomic.Int32
	c, err := NewCache(ctx, "", nil, func(context.Context) error {
		snapshotGCCalls.Add(1)
		return nil
	})
	assert.NilError(t, err)

	sizeCalls := &atomic.Int32{}
	results := make([]AnyResult, 0, 3)
	for i := range 3 {
		call := cacheTestIntCall(fmt.Sprintf("metadata-prune-order-%d", i))
		res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
			ResultCall:    call,
			IsPersistable: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestSizedIntResult(call, i, 1, fmt.Sprintf("snapshot://metadata-prune-%d", i), sizeCalls), nil
		})
		assert.NilError(t, err)
		results = append(results, res)
	}
	cacheTestReleaseSession(t, c, ctx)

	now := time.Now()
	c.egraphMu.Lock()
	for i, res := range results {
		shared := res.cacheSharedResult()
		shared.createdAtUnixNano = now.Add(time.Duration(i-3) * time.Hour).UnixNano()
		shared.lastUsedAtUnixNano = shared.createdAtUnixNano
	}
	// Expiry wins over LRU, so the middle result is selected first even though
	// the first result is older.
	edge := c.persistedEdgesByResult[results[1].cacheSharedResult().id]
	edge.expiresAtUnix = now.Add(-time.Minute).Unix()
	c.persistedEdgesByResult[edge.resultID] = edge
	c.egraphMu.Unlock()

	before := c.MetadataEstimate()
	directBytes := metadataDirectResultBytes(before)
	snapshot := c.snapshotPruneState(nil, pruneSnapshotMetadata, directBytes)
	for _, res := range snapshot.results {
		assert.Equal(t, directBytes, res.directResultBytes)
		assert.Equal(t, "", res.entry.ID)
		assert.Equal(t, "", res.entry.Description)
		assert.Equal(t, "", res.callLabel)
		assert.Assert(t, res.callFrame == nil)
		assert.Equal(t, 0, len(res.usageIdentities))
	}

	target := before.EstimatedBytes - 2*directBytes
	report, err := c.PruneMetadataEstimate(ctx, before.EstimatedBytes-1, target)
	assert.NilError(t, err)
	assert.Assert(t, report.Triggered)
	assert.Equal(t, 3, report.CandidateCount)
	assert.Equal(t, 2, report.PlannedRootCount)
	assert.Equal(t, 2, report.SimulatedCollectedResultCount)
	assert.Equal(t, 2, report.RemovedPersistedRootCount)
	assert.Equal(t, int32(0), sizeCalls.Load())
	assert.Equal(t, int32(1), snapshotGCCalls.Load())
	assert.Assert(t, report.SnapshotGCAttempted)
	assert.Assert(t, report.SnapshotGCSucceeded)
	assert.Assert(t, report.FinalCompactionOldClassSlots > report.FinalCompactionNewClassSlots)

	c.egraphMu.RLock()
	_, oldestPresent := c.resultsByID[results[0].cacheSharedResult().id]
	_, expiredPresent := c.resultsByID[results[1].cacheSharedResult().id]
	_, newestPresent := c.resultsByID[results[2].cacheSharedResult().id]
	c.egraphMu.RUnlock()
	assert.Assert(t, !oldestPresent)
	assert.Assert(t, !expiredPresent)
	assert.Assert(t, newestPresent)
}

func TestCachePruneMetadataEstimateCreditsCollectedDependencyClosure(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)

	depCall := cacheTestIntCall("metadata-prune-dependency")
	dep, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: depCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(depCall, 1), nil
	})
	assert.NilError(t, err)
	rootCall := cacheTestIntCall("metadata-prune-root")
	root, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{
		ResultCall:    rootCall,
		IsPersistable: true,
	}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(rootCall, 2), nil
	})
	assert.NilError(t, err)
	assert.NilError(t, c.AddExplicitDependency(ctx, root, dep, "metadata_prune_test"))
	cacheTestReleaseSession(t, c, ctx)

	before := c.MetadataEstimate()
	report, err := c.PruneMetadataEstimate(ctx, before.EstimatedBytes-1, 1)
	assert.NilError(t, err)
	assert.Equal(t, 1, report.CandidateCount)
	assert.Equal(t, 1, report.PlannedRootCount)
	assert.Equal(t, 2, report.SimulatedCollectedResultCount)
	assert.Equal(t, 1, report.RemovedPersistedRootCount)
	assert.DeepEqual(t, report.AfterPrune, CacheMetadataEstimate{})
}

func TestCachePruneMetadataEstimateProtectsActiveAndUnpruneableResults(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	newSessionCtx := func(name string) context.Context {
		return engine.ContextWithClientMetadata(baseCtx, &engine.ClientMetadata{ClientID: name, SessionID: name})
	}
	newPersistable := func(ctx context.Context, name string) AnyResult {
		call := cacheTestIntCall(name)
		res, err := c.GetOrInitCall(ctx, name, noopTypeResolver{}, &CallRequest{ResultCall: call, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(call, 1), nil
		})
		assert.NilError(t, err)
		return res
	}

	activeCtx := newSessionCtx("metadata-prune-active")
	active := newPersistable(activeCtx, "metadata-prune-active")
	coldCtx := newSessionCtx("metadata-prune-cold")
	cold := newPersistable(coldCtx, "metadata-prune-cold")
	assert.NilError(t, c.ReleaseSession(coldCtx, "metadata-prune-cold"))
	unpruneableCtx := newSessionCtx("metadata-prune-unpruneable")
	unpruneable := newPersistable(unpruneableCtx, "metadata-prune-unpruneable")
	assert.NilError(t, c.MakeResultUnpruneable(unpruneableCtx, unpruneable))
	assert.NilError(t, c.ReleaseSession(unpruneableCtx, "metadata-prune-unpruneable"))

	before := c.MetadataEstimate()
	report, err := c.PruneMetadataEstimate(baseCtx, before.EstimatedBytes-1, 1)
	assert.NilError(t, err)
	assert.Equal(t, 1, report.CandidateCount)
	assert.Equal(t, 1, report.RemovedPersistedRootCount)
	assert.Assert(t, report.CandidatesExhausted)
	assert.Assert(t, report.AfterPrune.EstimatedBytes > report.TargetEstimatedBytes)

	c.egraphMu.RLock()
	_, activePresent := c.resultsByID[active.cacheSharedResult().id]
	_, coldPresent := c.resultsByID[cold.cacheSharedResult().id]
	_, unpruneablePresent := c.resultsByID[unpruneable.cacheSharedResult().id]
	c.egraphMu.RUnlock()
	assert.Assert(t, activePresent)
	assert.Assert(t, !coldPresent)
	assert.Assert(t, unpruneablePresent)
	assert.NilError(t, c.ReleaseSession(activeCtx, "metadata-prune-active"))
}

func TestCachePruneMetadataEstimateCompactionCanRestoreMaximumWithoutEviction(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	call := cacheTestIntCall("metadata-prune-compact-only")
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: call, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(call, 1), nil
	})
	assert.NilError(t, err)
	cacheTestReleaseSession(t, c, ctx)

	c.egraphMu.Lock()
	for i := range 3 {
		c.ensureEqClassForDigestLocked(ctx, fmt.Sprintf("metadata-prune-dead-class-%d", i))
	}
	c.egraphMu.Unlock()

	before := c.MetadataEstimate()
	maximum := before.EstimatedBytes - 3*cacheMetadataClassSlotEstimatedBytes
	report, err := c.PruneMetadataEstimate(ctx, maximum, maximum-1)
	assert.NilError(t, err)
	assert.Assert(t, report.Triggered)
	assert.Equal(t, maximum, report.AfterInitialCompaction.EstimatedBytes)
	assert.Equal(t, 0, report.CandidateCount)
	assert.Equal(t, 0, report.PlannedRootCount)
	assert.Equal(t, 0, report.RemovedPersistedRootCount)
	assert.Assert(t, report.InitialCompactionOldClassSlots > report.InitialCompactionNewClassSlots)

	c.egraphMu.RLock()
	_, present := c.resultsByID[res.cacheSharedResult().id]
	c.egraphMu.RUnlock()
	assert.Assert(t, present)
}

func TestCachePruneMetadataEstimateMarksReleaseAndSnapshotGCContext(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	var releaseMarked atomic.Bool
	var snapshotGCMarked atomic.Bool
	c, err := NewCache(ctx, "", nil, func(gcCtx context.Context) error {
		snapshotGCMarked.Store(isMetadataPruneContext(gcCtx))
		return nil
	})
	assert.NilError(t, err)
	call := cacheTestIntCall("metadata-prune-context-marker")
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: call, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResultWithOnRelease(call, 1, func(releaseCtx context.Context) error {
			releaseMarked.Store(isMetadataPruneContext(releaseCtx))
			return nil
		}), nil
	})
	assert.NilError(t, err)
	cacheTestReleaseSession(t, c, ctx)

	before := c.MetadataEstimate()
	report, err := c.PruneMetadataEstimate(ctx, before.EstimatedBytes-1, 1)
	assert.NilError(t, err)
	assert.Assert(t, releaseMarked.Load())
	assert.Assert(t, snapshotGCMarked.Load())
	assert.Assert(t, report.SnapshotGCSucceeded)
}

func TestRemovePersistedEdgeRechecksUnpruneableAfterPlanning(t *testing.T) {
	for _, mode := range []pruneSnapshotMode{pruneSnapshotDisk, pruneSnapshotMetadata} {
		mode := mode
		t.Run(fmt.Sprintf("mode-%d", mode), func(t *testing.T) {
			ctx := cacheTestContext(t.Context())
			c, err := NewCache(ctx, "", nil, nil)
			assert.NilError(t, err)
			call := cacheTestIntCall("live-unpruneable-recheck")
			res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: call, IsPersistable: true}, func(context.Context) (AnyResult, error) {
				return cacheTestIntResult(call, 1), nil
			})
			assert.NilError(t, err)
			cacheTestReleaseSession(t, c, ctx)

			direct := int64(0)
			if mode == pruneSnapshotMetadata {
				direct = metadataDirectResultBytes(c.MetadataEstimate())
			}
			snapshot := c.snapshotPruneState(nil, mode, direct)
			candidates := c.collectPruneCandidates(ctx, 0, snapshot, nil, CachePrunePolicy{All: true}, time.Now())
			plan, _, _ := buildPrunePlan(snapshot, candidates, 1)
			assert.Assert(t, cmp.Len(plan, 1))

			assert.NilError(t, c.MakeResultUnpruneable(ctx, res))
			removed, err := c.removePersistedEdge(ctx, plan[0].candidate.resultID)
			assert.NilError(t, err)
			assert.Assert(t, !removed)

			c.egraphMu.RLock()
			edge, present := c.persistedEdgesByResult[res.cacheSharedResult().id]
			c.egraphMu.RUnlock()
			assert.Assert(t, present)
			assert.Assert(t, edge.unpruneable)
		})
	}
}

func TestCachePruneMetadataEstimateCancellationDoesNotMutate(t *testing.T) {
	t.Parallel()

	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	call := cacheTestIntCall("metadata-prune-canceled")
	_, err = c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: call, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(call, 1), nil
	})
	assert.NilError(t, err)
	cacheTestReleaseSession(t, c, ctx)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	before := c.MetadataEstimate()
	_, err = c.PruneMetadataEstimate(canceled, before.EstimatedBytes-1, 1)
	assert.ErrorIs(t, err, context.Canceled)
	assert.DeepEqual(t, before, c.MetadataEstimate())
	assert.Equal(t, 1, c.EntryStats().RetainedCalls)
}
