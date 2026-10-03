package dagql

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// storedPersistenceTestCloud opens a Cloud cache saved at path.
func storedPersistenceTestCloud(t *testing.T, path string) (context.Context, *Cache) {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, path, nil, nil, WithBlobStore())
	require.NoError(t, err)
	return ctx, c
}

// storedPersistenceTestMerge merges an export of roots from engine cache-a
// into the Cloud, and returns the Cloud's number of each root.
func storedPersistenceTestMerge(t *testing.T, ctx context.Context, cloud *Cache, bundle ValueBundle) []uint64 {
	t.Helper()
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	var numbers []uint64
	for _, root := range reply.Imported() {
		numbers = append(numbers, root.ResultID)
	}
	return numbers
}

// storedPersistenceTestEntry is what a save must keep of an entry.
type storedPersistenceTestEntry struct {
	retained                bool
	storedParts             map[string]PersistedPartOffer
	deps                    []uint64
	recordBytes             int64
	createdAt, lastUsed     int64
	envelopeHasStoredParts  bool
	expiresAtUnix           int64
	replacements            uint64
	indexedUnderItsOwnFrame bool
}

func storedPersistenceTestState(c *Cache) map[uint64]storedPersistenceTestEntry {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	out := map[uint64]storedPersistenceTestEntry{}
	for id, res := range c.resultsByID {
		_, retained := c.persistedEdgesByResult[id]
		var deps []uint64
		for dep := range res.deps {
			deps = append(deps, uint64(dep))
		}
		slices.Sort(deps)
		state := res.loadPayloadState()
		out[uint64(id)] = storedPersistenceTestEntry{
			retained:                retained,
			storedParts:             res.storedParts,
			deps:                    deps,
			recordBytes:             res.storedRecordBytes,
			createdAt:               state.createdAtUnixNano,
			lastUsed:                state.lastUsedAtUnixNano,
			envelopeHasStoredParts:  state.persistedEnvelope != nil && len(state.persistedEnvelope.StoredParts) > 0,
			expiresAtUnix:           res.expiresAtUnix,
			replacements:            res.replacements,
			indexedUnderItsOwnFrame: len(res.recipeKeys) > 0,
		}
	}
	return out
}

// A save and restore keeps the pool: the entries under their numbers, the
// stored roots, the dependencies, each value's stored parts, and its last
// use and creation times. The stored parts come back onto the entry, out of
// the record, which a bundle copies as it is.
func TestCloudSaveAndRestoreKeepThePool(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "saved-root", &transferTestValue{Text: "root"})
	p := persistedListTestResult(t, actx, a, asrv, "saved-dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, r, p)
	root := storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, actx, a, r))[0]
	stored, err := cloud.SetStoredPart(ctx, root, storedPruneTestPart("snapshot", map[string]int64{"saved-base": 10, "saved-top": 20}), 0)
	require.NoError(t, err)
	require.True(t, stored)
	storedPruneTestUse(cloud, root, time.Now().Add(-time.Hour))
	before := storedPersistenceTestState(cloud)
	require.Len(t, before, 2)
	cloud.egraphMu.RLock()
	poolBefore := cloud.poolEstimateLocked()
	cloud.egraphMu.RUnlock()
	require.NoError(t, cloud.Close(ctx))

	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	require.Equal(t, CachePersistenceResetNone, cloud.PersistenceResetReason())
	require.Equal(t, before, storedPersistenceTestState(cloud))
	cloud.egraphMu.RLock()
	poolAfter := cloud.poolEstimateLocked()
	cloud.egraphMu.RUnlock()
	require.Equal(t, poolBefore, poolAfter, "the record sizes come back as merge measured them")

	got, err := cloud.StoredBundle(ctx, []uint64{root})
	require.NoError(t, err)
	require.Len(t, got.Bundle.Values, 2)
	for _, value := range got.Bundle.Values {
		require.Empty(t, value.Record.Envelope.StoredParts, "records sent to engines carry no stored parts")
	}
	require.Len(t, got.Bundle.Outputs, 1)
	require.Len(t, got.Bundle.Outputs[0].Chain.Layers, 2)

	// New entries take numbers the saved ones never had.
	again, _ := mergeTestSource(t, "after-restore", "new")
	next := storedPersistenceTestMerge(t, ctx, cloud, again)[0]
	require.NotContains(t, before, next)
}

// The restore keeps every record as it was saved, and decodes none, so a
// restored closure is placed whatever it holds: an object, a list, a scalar
// and a null value. An engine's restore decodes the scalar and the null at
// once and drops their records.
func TestCloudRestoreKeepsEveryRecord(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	actx, a, asrv := transferTestCache(t)
	absent := persistedListTestResult(t, actx, a, asrv, "absent-int", DynamicNullable{Elem: Int(0)})
	scalar := persistedListTestResult(t, actx, a, asrv, "present-int", Int(3))
	list := persistedListTestResult(t, actx, a, asrv, "nullable-int-list", DynamicResultArrayOutput{
		Elem:   DynamicNullable{Elem: Int(0)},
		Values: []AnyResult{absent, scalar},
	})
	r := persistedListTestResult(t, actx, a, asrv, "list-holder", &transferTestValue{Text: "holder"})
	transferTestDependency(a, actx, r, list)
	root := storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, actx, a, r))[0]
	before, err := cloud.StoredBundle(ctx, []uint64{root})
	require.NoError(t, err)
	kinds := func(bundle ValueBundle) []string {
		var out []string
		for _, value := range bundle.Values {
			out = append(out, value.Record.Envelope.Kind)
		}
		slices.Sort(out)
		return out
	}
	require.Equal(t, []string{persistedResultKindList, persistedResultKindNull, persistedResultKindObject, persistedResultKindScalar}, kinds(before.Bundle))
	require.NoError(t, cloud.Close(ctx))

	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	after, err := cloud.StoredBundle(ctx, []uint64{root})
	require.NoError(t, err, "every record of the closure is still stored")
	require.Equal(t, kinds(before.Bundle), kinds(after.Bundle))
	require.Equal(t, before.Bundle.Values, after.Bundle.Values)

	bctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, after.Bundle)
	require.NoError(t, err, "the restored bundle merges into an engine")
	require.True(t, reply.Committed)
}

// A blob-backed cache restores its last save whatever ended the process:
// a store checkpointed and never closed comes back, without what changed
// after the save. An engine's cache still wipes a store it never closed.
func TestCloudRestoresAStoreItNeverClosed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	saved, _ := mergeTestSource(t, "checkpointed", "saved")
	root := storedPersistenceTestMerge(t, ctx, cloud, saved)[0]
	require.NoError(t, cloud.Checkpoint(ctx))
	require.Equal(t, 1, cloud.PersistedResults())
	lost, _ := mergeTestSource(t, "after-checkpoint", "lost")
	later := storedPersistenceTestMerge(t, ctx, cloud, lost)[0]
	// The process dies: nothing saves again or marks the store clean.
	require.NoError(t, cloud.CloseDiscardingPersistence())

	ctx, cloud = storedPersistenceTestCloud(t, path)
	require.Equal(t, CachePersistenceResetNone, cloud.PersistenceResetReason())
	state := storedPersistenceTestState(cloud)
	require.Contains(t, state, root)
	require.True(t, state[root].retained)
	require.NotContains(t, state, later, "what changed after the last save is lost")
	// It dies again, before any save: the same save comes back.
	require.NoError(t, cloud.CloseDiscardingPersistence())
	ctx, cloud = storedPersistenceTestCloud(t, path)
	require.Contains(t, storedPersistenceTestState(cloud), root)
	require.NoError(t, cloud.Close(ctx))
	require.ErrorIs(t, cloud.Checkpoint(ctx), ErrCacheClosed)

	enginePath := filepath.Join(t.TempDir(), "engine.db")
	ectx := cacheTestContext(t.Context())
	engine, err := NewCache(ectx, enginePath, nil, nil)
	require.NoError(t, err)
	require.ErrorContains(t, engine.Checkpoint(ectx), "no blob store")
	require.NoError(t, engine.CloseDiscardingPersistence())
	engine, err = NewCache(ectx, enginePath, nil, nil)
	require.NoError(t, err)
	require.Equal(t, CachePersistenceResetUncleanShutdown, engine.PersistenceResetReason())
	require.NoError(t, engine.CloseDiscardingPersistence())
}
