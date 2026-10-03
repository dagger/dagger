package dagql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// storedPruneTestState is whether the Cloud entry with the number is
// registered, and whether it has a value, stored parts, dependencies and a
// retention edge.
type storedPruneTestState struct {
	registered, value, parts, deps, retained bool
}

func storedPruneTestEntryState(c *Cache, number uint64) storedPruneTestState {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[sharedResultID(number)]
	if res == nil {
		return storedPruneTestState{}
	}
	_, retained := c.persistedEdgesByResult[res.id]
	return storedPruneTestState{
		registered: true,
		value:      res.hasOwnValueLocked(),
		parts:      len(res.storedParts) > 0,
		deps:       len(res.deps) > 0,
		retained:   retained,
	}
}

// storedPruneTestRoot merges into the Cloud, from cache-a, an export of a
// root of its own recipe, and returns its number.
func storedPruneTestRoot(t *testing.T, ctx context.Context, cloud *Cache, field string) uint64 {
	t.Helper()
	bundle, _ := mergeTestSource(t, field, field)
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	return reply.Imported()[0].ResultID
}

// On a blob-backed cache, a prune that drops a stored root drops every value
// that only holdings still keep: the root's record and stored parts, and its
// dependency's, which only the root's value and the sender's holding kept.
// Their entries stay, known only through holdings, and go when the holdings
// do. The report counts the dropped values.
func TestPruneDropsValuesOnlyHoldingsKeep(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	stored, err := cloud.SetStoredPart(ctx, r, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	require.Equal(t, storedPruneTestState{registered: true, value: true, parts: true, deps: true, retained: true}, storedPruneTestEntryState(cloud, r))

	report, err := cloud.PruneMetadataEstimate(ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, 1, report.RemovedPersistedRootCount)
	require.Equal(t, 2, report.DroppedValues, "R's value and P's")
	for _, number := range []uint64{r, p} {
		require.Equal(t, storedPruneTestState{registered: true}, storedPruneTestEntryState(cloud, number), "the entry stays for its holding, with no value")
		cloud.egraphMu.RLock()
		noValue := storedBundleTestEntry(cloud, number).noValueLocked()
		cloud.egraphMu.RUnlock()
		require.True(t, noValue)
	}
	got, err := cloud.StoredBundle(ctx, []uint64{r})
	require.NoError(t, err)
	require.Equal(t, []uint64{r}, got.NoValue, "a dropped value is not placed")

	_, err = cloud.CollectRemoteHoldings(ctx, cloud.ReleaseRemoteCache(ctx, "cache-a"))
	require.NoError(t, err)
	for _, number := range []uint64{r, p} {
		require.Equal(t, storedPruneTestState{}, storedPruneTestEntryState(cloud, number), "collected with its last holding")
	}
}

// A value another value still depends on stays: dropping one of two roots
// that share a dependency drops that root's value only, and dropping the
// other drops both.
func TestPruneKeepsValuesOtherValuesNeed(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	actx, a, asrv := transferTestCache(t)
	r2 := persistedListTestResult(t, actx, a, asrv, "second-root", &transferTestValue{Text: "second"})
	dep := persistedListTestResult(t, actx, a, asrv, "bundle-dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, r2, dep)
	reply, err := cloud.MergeValues(ctx, "cache-a", exportTestBundle(t, actx, a, r2))
	require.NoError(t, err)
	second := reply.Imported()[0].ResultID
	require.Contains(t, storedPartTestDeps(cloud, second), p, "both roots depend on P")

	_, removed, dropped, err := cloud.removePrunedEdge(ctx, sharedResultID(r))
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, 1, dropped, "R's value only")
	require.False(t, storedPruneTestEntryState(cloud, r).value)
	require.True(t, storedPruneTestEntryState(cloud, p).value, "the second root needs P")

	_, removed, dropped, err = cloud.removePrunedEdge(ctx, sharedResultID(second))
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, 2, dropped, "the second root's value and P's")
	require.False(t, storedPruneTestEntryState(cloud, p).value)
}

// An entry a prune left with no value is collected with its last holding, and
// one that still has a holding takes the record again from a later export,
// under the same number.
func TestPrunedEntryStoresAgain(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	_, removed, dropped, err := cloud.removePrunedEdge(ctx, sharedResultID(r))
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, 2, dropped)

	_, found, err := cloud.CollectRemoteHolding(ctx, HolderKey{Cache: "cache-a", Number: bundleSenderNumberForTest(t, cloud, p)})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, storedPruneTestState{}, storedPruneTestEntryState(cloud, p), "no holding and no value: collected")

	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "bundle-root", &transferTestValue{Text: "root"})
	dep := persistedListTestResult(t, actx, a, asrv, "bundle-dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, root, dep)
	reply, err := cloud.MergeValues(ctx, "cache-b", exportTestBundle(t, actx, a, root))
	require.NoError(t, err)
	require.Equal(t, r, reply.Imported()[0].ResultID, "the same entry")
	require.Equal(t, storedPruneTestState{registered: true, value: true, deps: true, retained: true}, storedPruneTestEntryState(cloud, r))
}

// Outside a prune, ownership is unchanged: the part a later step stores can
// name a record that only its sender's holding keeps. But a part that names an
// entry a prune left with no value is refused, with no dependency added: the
// value that stored it could never be placed.
func TestSetStoredPartRefusesAnEntryAPruneEmptied(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	q := storedPruneTestRoot(t, ctx, cloud, "other-root")
	_, removed, _, err := cloud.removePrunedEdge(ctx, sharedResultID(r))
	require.NoError(t, err)
	require.True(t, removed)
	require.True(t, storedPruneTestEntryState(cloud, p).registered)
	require.False(t, storedPruneTestEntryState(cloud, p).value)

	deps := storedPartTestDeps(cloud, q)
	stored, err := cloud.SetStoredPart(ctx, q, storedPartNaming(p), 0)
	require.NoError(t, err)
	require.False(t, stored)
	require.Equal(t, deps, storedPartTestDeps(cloud, q))
	require.False(t, storedPruneTestEntryState(cloud, q).parts)

	got, err := cloud.StoredBundle(ctx, []uint64{q})
	require.NoError(t, err)
	require.Len(t, got.Bundle.Values, 1, "Q's bundle still builds")
}
