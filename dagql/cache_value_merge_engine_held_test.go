package dagql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// engineHeldTestBundle is bundle with the row of each of held sent as a held
// row: its recipe and sending facts, with no record and no dependencies.
func engineHeldTestBundle(t *testing.T, c *Cache, bundle ValueBundle, held ...AnyResult) ValueBundle {
	t.Helper()
	out := bundle
	out.Values = append([]TransferredValue(nil), bundle.Values...)
	for _, res := range held {
		shared := res.cacheSharedResult()
		recipe, err := shared.loadResultCall().deriveRecipeDigest(c)
		require.NoError(t, err)
		found := false
		for i, row := range out.Values {
			if row.SenderNumber != uint64(shared.id) {
				continue
			}
			out.Values[i] = TransferredValue{Ordinal: row.Ordinal, SenderNumber: row.SenderNumber, SenderReplacements: row.SenderReplacements, Record: PersistedRecord{ResultID: uint64(row.Ordinal)}, ExpiresAtUnix: row.ExpiresAtUnix, Held: recipe}
			found = true
		}
		require.True(t, found, "the bundle carries entry %d", shared.id)
	}
	return out
}

// cloudEntryOfRecipe is the Cloud's current entry for res's recipe, and the
// numbers of its dependencies.
func cloudEntryOfRecipe(t *testing.T, cloud *Cache, engine *Cache, res AnyResult) (*sharedResult, []sharedResultID) {
	t.Helper()
	recipe, err := res.cacheSharedResult().loadResultCall().deriveRecipeDigest(engine)
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	entry := cloud.currentEntryForRecipeLocked(recipe)
	if entry == nil {
		return nil, nil
	}
	var deps []sharedResultID
	for dep := range entry.deps {
		deps = append(deps, dep)
	}
	return entry, deps
}

// The Cloud merges an engine's bundle that sends a row the Cloud already
// holds as a held row: the rows above it land as they do with the row in
// full, depending on the Cloud's entry for the held row's recipe.
func TestCloudMergesAnEnginesHeldRows(t *testing.T) {
	t.Parallel()
	ctx, engine, srv := transferTestCache(t)
	child := persistedListTestResult(t, ctx, engine, srv, "held-child", String("child"))
	root := persistedListTestResult(t, ctx, engine, srv, "held-root", String("root"))
	transferTestDependency(engine, ctx, root, child)
	childOnly := exportTestBundle(t, ctx, engine, child)
	full := exportTestBundle(t, ctx, engine, root)
	require.Len(t, full.Values, 2)
	held := engineHeldTestBundle(t, engine, full, child)

	merge := func(t *testing.T, cloud *Cache, cctx context.Context, bundle ValueBundle) MergeReply {
		t.Helper()
		reply, err := cloud.MergeValues(cctx, "engine", bundle)
		require.NoError(t, err)
		return reply
	}
	inFullCtx, inFull := storedPartTestCache(t, WithBlobStore())
	require.True(t, merge(t, inFull, inFullCtx, childOnly).Committed)
	require.True(t, merge(t, inFull, inFullCtx, full).Committed)

	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	require.True(t, merge(t, cloud, cctx, childOnly).Committed)
	reply := merge(t, cloud, cctx, held)
	require.True(t, reply.Committed)
	require.Empty(t, reply.Stale)

	childEntry, _ := cloudEntryOfRecipe(t, cloud, engine, child)
	require.NotNil(t, childEntry)
	rootEntry, rootDeps := cloudEntryOfRecipe(t, cloud, engine, root)
	require.NotNil(t, rootEntry, "the row above the held row lands")
	require.Equal(t, []sharedResultID{childEntry.id}, rootDeps, "on the Cloud's entry for the held row's recipe")
	cloud.egraphMu.RLock()
	entries := len(cloud.resultsByID)
	cloud.egraphMu.RUnlock()
	inFull.egraphMu.RLock()
	inFullEntries := len(inFull.resultsByID)
	inFull.egraphMu.RUnlock()
	require.Equal(t, inFullEntries, entries, "as merging the row in full does")
}

// A held row the Cloud has no live entry for refuses the engine's whole
// bundle as stale, naming the row, and the merge commits nothing.
func TestCloudRefusesAnEnginesStaleHeldRow(t *testing.T) {
	t.Parallel()
	ctx, engine, srv := transferTestCache(t)
	child := persistedListTestResult(t, ctx, engine, srv, "stale-child", String("child"))
	root := persistedListTestResult(t, ctx, engine, srv, "stale-root", String("root"))
	transferTestDependency(engine, ctx, root, child)
	full := exportTestBundle(t, ctx, engine, root)
	held := engineHeldTestBundle(t, engine, full, child)
	var heldOrdinal TransferOrdinal
	for _, row := range held.Values {
		if row.Held != "" {
			heldOrdinal = row.Ordinal
		}
	}

	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(cctx, "engine", held)
	require.NoError(t, err)
	require.False(t, reply.Committed)
	require.Equal(t, []TransferOrdinal{heldOrdinal}, reply.Stale)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	require.Empty(t, cloud.resultsByID, "nothing commits")
}
