package dagql

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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
	payload, _ := cacheTestPayloadTotalConsistent(cloud)
	require.Positive(t, payload, "the records' envelopes")

	report, err := cloud.PruneMetadataEstimate(ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, 1, report.RemovedPersistedRootCount)
	require.Equal(t, 2, report.DroppedValues, "R's value and P's")
	payload, sum := cacheTestPayloadTotalConsistent(cloud)
	require.Equal(t, sum, payload)
	require.Zero(t, payload, "a dropped value's envelope goes with it")
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

// storedPruneTestUse sets the last use of the Cloud entry with the number.
func storedPruneTestUse(c *Cache, number uint64, at time.Time) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	res := c.resultsByID[sharedResultID(number)]
	res.payloadMu.Lock()
	res.lastUsedAtUnixNano = at.UnixNano()
	res.payloadMu.Unlock()
}

// storedPruneTestPart is a stored part whose chain has the layers, each blob
// of the size.
func storedPruneTestPart(part string, layers map[string]int64) PersistedPartOffer {
	offer := PersistedPartOffer{Address: PersistedPartAddress{Part: PartKey(part)}, Value: SnapshotValue{Kind: "directory", Path: "/"}}
	names := slices.Sorted(maps.Keys(layers))
	for _, name := range names {
		offer.Chain.Layers = append(offer.Chain.Layers, snapshots.ExportLayer{Descriptor: ocispec.Descriptor{
			MediaType: "application/vnd.oci.image.layer.v1.tar+gzip",
			Digest:    testDigest(name),
			Size:      layers[name],
		}})
	}
	return offer
}

// storedPruneTestPlace places every stored root of the Cloud on engine
// cache-b, as placement does: a merged reply for each root's closure, with
// the root retained.
func storedPruneTestPlace(t *testing.T, ctx context.Context, cloud *Cache, roots []uint64) {
	t.Helper()
	built, err := cloud.StoredBundle(ctx, roots)
	require.NoError(t, err)
	reply := MergeReply{Generation: 1, EngineTimeUnixNano: time.Now().UnixNano()}
	for _, value := range built.Bundle.Values {
		var deps []uint64
		for _, dep := range value.DependencyIDs {
			deps = append(deps, 1000+dep)
		}
		reply.Values = append(reply.Values, MergedValue{Ordinal: value.Ordinal, Number: 1000 + uint64(value.Ordinal), Deps: deps})
	}
	for _, root := range built.Bundle.Roots {
		reply.Roots = append(reply.Roots, MergedRoot{Ordinal: root.Ordinal, Number: 1000 + uint64(root.Ordinal), Retained: true})
	}
	_, err = cloud.ApplyMergedReply(ctx, "cache-b", built.Bundle, reply)
	require.NoError(t, err)
}

// The memory stage measures a blob-backed cache's pool: each value in the
// closure of the stored roots, as its record's bytes plus an entry's 3 KiB.
// An entry known only through holdings, and a value outside the closure,
// count for nothing.
func TestPoolEstimateCountsTheStoredRootsClosure(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, service := storedPartServiceTestCloud(t)
	_, err := cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-x", Number: 3}, holdingOf("holdings-only"))
	require.NoError(t, err)

	cloud.egraphMu.RLock()
	estimate := cloud.poolEstimateLocked()
	var sizes []int64
	for _, number := range []uint64{r, storedPartTestDeps(cloud, r)[0]} {
		sizes = append(sizes, cloud.resultsByID[sharedResultID(number)].storedRecordBytes)
	}
	serviceBytes := cloud.resultsByID[sharedResultID(service)].storedRecordBytes
	cloud.egraphMu.RUnlock()
	var want int64
	for _, size := range sizes {
		require.Positive(t, size, "a merged record has its size")
		want += size + cacheMetadataResultEstimatedBytes
	}
	require.Equal(t, 2, estimate.ResultCount, "R and P; not the service, which only its sender's holding keeps")
	require.Equal(t, want, estimate.EstimatedBytes)
	require.Positive(t, serviceBytes, "a stored record outside the pool still has its size")
}

// A pool whose every root another engine holds, as placement leaves it, is
// pruned to its target by dropping the fewest least recently used roots: it
// is not emptied. Without the prune going through holdings, no drop would
// free anything and every root would go.
func TestPruneDropsTheLeastRecentlyUsedRootsToTheTarget(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	start := time.Now().Add(-time.Hour)
	var roots []uint64
	for i := range 10 {
		root := storedPruneTestRoot(t, ctx, cloud, fmt.Sprintf("pool-root-%02d", i))
		storedPruneTestUse(cloud, root, start.Add(time.Duration(i)*time.Minute))
		roots = append(roots, root)
	}
	storedPruneTestPlace(t, ctx, cloud, roots)

	cloud.egraphMu.RLock()
	before := cloud.poolEstimateLocked()
	perRoot := cloud.resultsByID[sharedResultID(roots[0])].storedValueBytes()
	cloud.egraphMu.RUnlock()
	require.Equal(t, 10, before.ResultCount)
	// Dropping three roots reaches the target, two don't.
	target := before.EstimatedBytes - 3*perRoot + perRoot/2
	report, err := cloud.PruneMetadataEstimate(ctx, before.EstimatedBytes-1, target)
	require.NoError(t, err)
	require.True(t, report.Triggered)
	require.False(t, report.CandidatesExhausted)
	require.Equal(t, 3, report.RemovedPersistedRootCount)
	require.Equal(t, 3, report.DroppedValues)
	require.Equal(t, 7, report.AfterPrune.ResultCount)
	for i, root := range roots {
		state := storedPruneTestEntryState(cloud, root)
		require.True(t, state.registered, "the holdings keep every entry")
		require.Equal(t, i >= 3, state.value, "root %d: the three least recently used go", i)
	}
}

// The disk stage counts the distinct blobs the pool's stored parts name, each
// once however many values share it, and frees a blob only when its last
// value goes. Expired roots go first.
func TestPruneCountsSharedBlobsOnce(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	older := storedPruneTestRoot(t, ctx, cloud, "blob-root-older")
	newer := storedPruneTestRoot(t, ctx, cloud, "blob-root-newer")
	storedPruneTestUse(cloud, older, time.Now().Add(-2*time.Hour))
	storedPruneTestUse(cloud, newer, time.Now().Add(-time.Hour))
	for number, layers := range map[uint64]map[string]int64{
		older: {"base": 100, "older-top": 50},
		newer: {"base": 100, "newer-top": 30},
	} {
		stored, err := cloud.SetStoredPart(ctx, number, storedPruneTestPart("snapshot", layers), 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	storedPruneTestPlace(t, ctx, cloud, []uint64{older, newer})
	require.Equal(t, int64(180), cloud.snapshotPruneState(pruneSnapshotDisk, 0).usedBytes, "the shared base counts once")

	report, err := cloud.Prune(ctx, []CachePrunePolicy{{MaxUsedSpace: 179, TargetSpace: 140}})
	require.NoError(t, err)
	require.Len(t, report.Entries, 1)
	require.Equal(t, int64(50), report.ReclaimedBytes, "the older root's own layer; the base stays")
	require.Equal(t, 1, report.DroppedValues)
	require.False(t, storedPruneTestEntryState(cloud, older).value)
	require.True(t, storedPruneTestEntryState(cloud, newer).value)
	require.Equal(t, int64(130), cloud.snapshotPruneState(pruneSnapshotDisk, 0).usedBytes)

	report, err = cloud.Prune(ctx, []CachePrunePolicy{{MaxUsedSpace: 129, TargetSpace: 10}})
	require.NoError(t, err)
	require.Equal(t, int64(130), report.ReclaimedBytes, "the last value frees the base too")
	require.Zero(t, cloud.snapshotPruneState(pruneSnapshotDisk, 0).usedBytes)
}

// An expired root goes before a less recently used one.
func TestPruneDropsExpiredRootsFirst(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	older := storedPruneTestRoot(t, ctx, cloud, "expiry-root-older")
	expired := storedPruneTestRoot(t, ctx, cloud, "expiry-root-expired")
	storedPruneTestUse(cloud, older, time.Now().Add(-2*time.Hour))
	storedPruneTestUse(cloud, expired, time.Now().Add(-time.Hour))
	for number, layers := range map[uint64]map[string]int64{
		older:   {"older-layer": 100},
		expired: {"expired-layer": 100},
	} {
		stored, err := cloud.SetStoredPart(ctx, number, storedPruneTestPart("snapshot", layers), 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	cloud.egraphMu.Lock()
	edge := cloud.persistedEdgesByResult[sharedResultID(expired)]
	edge.expiresAtUnix = time.Now().Add(-time.Minute).Unix()
	cloud.persistedEdgesByResult[sharedResultID(expired)] = edge
	cloud.egraphMu.Unlock()

	report, err := cloud.Prune(ctx, []CachePrunePolicy{{MaxUsedSpace: 150, TargetSpace: 120}})
	require.NoError(t, err)
	require.Len(t, report.Entries, 1)
	require.False(t, storedPruneTestEntryState(cloud, expired).value)
	require.True(t, storedPruneTestEntryState(cloud, older).value)
}
