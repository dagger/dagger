package dagql

import (
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// storedHoldingsTestInfo returns the holding's description.
func storedHoldingsTestInfo(t *testing.T, cloud *Cache, key HolderKey) RemoteHoldingInfo {
	t.Helper()
	info, ok := cloud.RemoteEntryInfo(key)
	require.True(t, ok)
	for _, h := range info.Holdings {
		if h.Key == key {
			return h
		}
	}
	t.Fatalf("no holding %v", key)
	return RemoteHoldingInfo{}
}

// storedHoldingsTestLastUsed returns the last use of the entry with the
// number.
func storedHoldingsTestLastUsed(cloud *Cache, number uint64) int64 {
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	return cloud.resultsByID[sharedResultID(number)].loadPayloadState().lastUsedAtUnixNano
}

// A holding is marked executed by a call span with executed evidence, not by
// a hit, a merged reply or an export's records. A prune that drops the
// entry's value clears the mark on every holding of the entry; a later
// computation sets it again.
func TestExecutedHoldings(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	info, ok := cloud.EntryInfo(r)
	require.True(t, ok)
	span := func(executed bool) RemoteHolding {
		return RemoteHolding{Recipe: info.Recipes[0], Field: "bundle-root", TypeName: "transferTestValue", Executed: executed}
	}
	sender := HolderKey{Cache: "cache-a", Number: bundleSenderNumberForTest(t, cloud, r)}
	require.False(t, storedHoldingsTestInfo(t, cloud, sender).Executed, "an export's records don't mark it")

	hit := HolderKey{Cache: "cache-b", Number: 7}
	_, err := cloud.AttachRemoteHolding(ctx, hit, span(false))
	require.NoError(t, err)
	require.False(t, storedHoldingsTestInfo(t, cloud, hit).Executed, "a hit doesn't mark it")
	storedPruneTestPlace(t, ctx, cloud, []uint64{r})
	placed := storedHoldingsTestKey(t, cloud, "cache-b", r, hit)
	require.False(t, storedHoldingsTestInfo(t, cloud, placed).Executed, "a merged reply doesn't mark it")

	_, err = cloud.AttachRemoteHolding(ctx, sender, span(true))
	require.NoError(t, err)
	require.True(t, storedHoldingsTestInfo(t, cloud, sender).Executed)
	_, err = cloud.AttachRemoteHolding(ctx, sender, span(false))
	require.NoError(t, err)
	require.True(t, storedHoldingsTestInfo(t, cloud, sender).Executed, "a later hit keeps the mark")

	_, removed, dropped, err := cloud.removePrunedEdge(ctx, sharedResultID(r))
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, 2, dropped)
	for _, key := range []HolderKey{sender, hit, placed} {
		require.False(t, storedHoldingsTestInfo(t, cloud, key).Executed, "the drop clears %v", key)
	}

	_, err = cloud.AttachRemoteHolding(ctx, hit, span(true))
	require.NoError(t, err)
	require.True(t, storedHoldingsTestInfo(t, cloud, hit).Executed, "a computation after the drop")
	require.False(t, storedHoldingsTestInfo(t, cloud, sender).Executed)
}

// storedHoldingsTestKey returns the key of cache's holding on the entry with
// the number, other than except.
func storedHoldingsTestKey(t *testing.T, cloud *Cache, cache CacheID, number uint64, except HolderKey) HolderKey {
	t.Helper()
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	for key, id := range cloud.holderEntries {
		if key.Cache == cache && uint64(id) == number && key != except {
			return key
		}
	}
	t.Fatalf("no %s holding on %d", cache, number)
	return HolderKey{}
}

// A call span uses its entry at the time the service read it: the entry's
// last use takes the latest such time, and never moves back. An export that
// stores the value uses it too.
func TestRowsSetTheLastUse(t *testing.T) {
	t.Parallel()
	start := time.Now()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	require.GreaterOrEqual(t, storedHoldingsTestLastUsed(cloud, r), start.UnixNano(), "the export stored it")

	info, ok := cloud.EntryInfo(r)
	require.True(t, ok)
	key := HolderKey{Cache: "cache-b", Number: 3}
	later := time.Now().Add(time.Hour).UnixNano()
	for _, at := range []int64{later, later - int64(time.Minute), 0} {
		_, err := cloud.AttachRemoteHolding(ctx, key, RemoteHolding{Recipe: info.Recipes[0], Field: "bundle-root", UsedAtUnixNano: at})
		require.NoError(t, err)
		require.Equal(t, later, storedHoldingsTestLastUsed(cloud, r))
	}
}

// DropStoredParts removes every stored part whose chain names the blob, on
// every entry, and nothing else: the entries' other parts, values and
// dependencies stay.
func TestDropStoredParts(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	q := storedPruneTestRoot(t, ctx, cloud, "other-root")
	for _, stored := range []struct {
		number uint64
		part   PersistedPartOffer
	}{
		{r, storedPruneTestPart("snapshot", map[string]int64{"gone-base": 10, "r-top": 5})},
		{r, storedPruneTestPart("mount:/kept", map[string]int64{"kept-layer": 7})},
		{q, storedPruneTestPart("snapshot", map[string]int64{"gone-base": 10})},
		{p, storedPruneTestPart("snapshot", map[string]int64{"p-layer": 3})},
	} {
		ok, err := cloud.SetStoredPart(ctx, stored.number, stored.part, 0)
		require.NoError(t, err)
		require.True(t, ok)
	}
	deps := storedPartTestDeps(cloud, r)

	dropped, err := cloud.DropStoredParts(testDigest("gone-base"))
	require.NoError(t, err)
	require.Equal(t, []StoredPart{
		{Number: r, Address: PersistedPartAddress{Part: "snapshot"}},
		{Number: q, Address: PersistedPartAddress{Part: "snapshot"}},
	}, dropped)
	state, err := cloud.StoredState()
	require.NoError(t, err)
	require.Equal(t, map[uint64][]PersistedPartAddress{
		r: {{Part: "mount:/kept"}},
		p: {{Part: "snapshot"}},
	}, state.Parts)
	require.Equal(t, deps, storedPartTestDeps(cloud, r))
	for _, number := range []uint64{r, q, p} {
		require.True(t, storedPruneTestEntryState(cloud, number).value)
	}

	none, err := cloud.DropStoredParts(testDigest("never-stored"))
	require.NoError(t, err)
	require.Empty(t, none)
}

// The queries the service builds its maps, its sweep and its memory estimate
// on: the stored roots and parts; every blob a stored part names, in the pool
// or not; and the counts the limits weigh.
func TestStoredQueries(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, service := storedPartServiceTestCloud(t)
	p := storedPartTestDeps(cloud, r)[0]
	for _, stored := range []struct {
		number uint64
		part   PersistedPartOffer
	}{
		{r, storedPruneTestPart("snapshot", map[string]int64{"shared": 100, "r-top": 10})},
		{p, storedPruneTestPart("snapshot", map[string]int64{"shared": 100})},
		{service, storedPruneTestPart("snapshot", map[string]int64{"outside-pool": 1000})},
	} {
		ok, err := cloud.SetStoredPart(ctx, stored.number, stored.part, 0)
		require.NoError(t, err)
		require.True(t, ok)
	}
	_, err := cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-x", Number: 3}, holdingOf("holdings-only"))
	require.NoError(t, err)

	state, err := cloud.StoredState()
	require.NoError(t, err)
	require.Equal(t, []uint64{r}, state.Roots)
	require.Len(t, state.Parts, 3)

	live, err := cloud.LiveBlobs()
	require.NoError(t, err)
	require.ElementsMatch(t, []digest.Digest{testDigest("shared"), testDigest("r-top"), testDigest("outside-pool")}, live, "a value outside the pool keeps its blobs live")

	usage, err := cloud.CloudUsage()
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	recordBytes := func(number uint64) int64 { return cloud.resultsByID[sharedResultID(number)].storedRecordBytes }
	want := CloudUsage{
		PoolValues:    2,
		PoolBytes:     recordBytes(r) + recordBytes(p) + 2*cacheMetadataResultEstimatedBytes,
		PoolBlobs:     2,
		PoolBlobBytes: 110,
		Entries:       len(cloud.resultsByID),
		Terms:         len(cloud.egraphTerms),
		ClassSlots:    cloud.eqClassSlotsLocked(),
		Holdings:      len(cloud.holderEntries),
		RecordBytes:   recordBytes(r) + recordBytes(p) + recordBytes(service),
	}
	cloud.egraphMu.RUnlock()
	require.Equal(t, want, usage)
	require.Equal(t, 4, usage.Entries, "R, P, the service and the holdings-only entry")
	require.Equal(t, 5, usage.Holdings, "cache-a's on R and P, cache-b's on R and the service, cache-x's")

	_, c := storedPartTestCache(t)
	_, err = c.StoredState()
	require.ErrorContains(t, err, "no blob store")
	_, err = c.LiveBlobs()
	require.ErrorContains(t, err, "no blob store")
	_, err = c.CloudUsage()
	require.ErrorContains(t, err, "no blob store")
	_, err = c.DropStoredParts(testDigest("x"))
	require.ErrorContains(t, err, "no blob store")
}
