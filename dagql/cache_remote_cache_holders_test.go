package dagql

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// requireCacheHoldersExact checks every entry's index of its holdings by
// engine cache (cacheHolders) against a recomputation from its holdings:
// each engine cache's holding numbers, the Cloud's own holding left out.
func requireCacheHoldersExact(t *testing.T, c *Cache) {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for id, entry := range c.resultsByID {
		want := map[CacheID]map[uint64]struct{}{}
		for key := range entry.holders {
			if key.Cache == cloudCacheID {
				continue
			}
			if want[key.Cache] == nil {
				want[key.Cache] = map[uint64]struct{}{}
			}
			want[key.Cache][key.Number] = struct{}{}
		}
		got := entry.cacheHolders
		if got == nil {
			got = map[CacheID]map[uint64]struct{}{}
		}
		require.Equal(t, want, got, "entry %d", id)
	}
}

// The index of holdings by engine cache stays exactly what the holdings say
// through attaching, holds, a session's release and its collection, a
// cache's release, the release of everything, which resets the e-graph, and
// attaching again after the reset.
func TestCacheHoldersIndexExact(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engineA, engineB, engineC := CacheID("cache-a"), CacheID("cache-b"), CacheID("cache-c")
	a.call(callRow{key: HolderKey{engineA, 1}, session: "a", holding: holdingOf("dep")})
	a.call(callRow{key: HolderKey{engineA, 2}, session: "a", holding: holdingOf("root", 1), retained: retainedAt(1, 1, 0)})
	// Two holdings of one cache on one entry.
	a.call(callRow{key: HolderKey{engineA, 3}, session: "a2", holding: holdingOf("root")})
	a.call(callRow{key: HolderKey{engineB, 1}, session: "b", holding: holdingOf("root"), retained: retainedAt(1, 1, 0)})
	a.call(callRow{key: HolderKey{engineB, 2}, session: "b", holding: holdingOf("dep")})
	a.call(callRow{key: HolderKey{engineC, 7}, session: "c", holding: holdingOf("other")})
	require.Empty(t, a.collect())
	requireCacheHoldersExact(t, cloud)

	a.endSession(engineA, "a2")
	require.Contains(t, a.collect(), HolderKey{engineA, 3}, "the session's own holding goes")
	requireCacheHoldersExact(t, cloud)

	a.endSession(engineB, "b")
	a.collect()
	requireCacheHoldersExact(t, cloud)

	a.candidates = append(a.candidates, cloud.ReleaseRemoteCache(t.Context(), engineA)...)
	a.collect()
	requireCacheHoldersExact(t, cloud)

	for _, cache := range []CacheID{engineB, engineC} {
		a.candidates = append(a.candidates, cloud.ReleaseRemoteCache(t.Context(), cache)...)
	}
	a.collect()
	keys, _ := cloudIndexes(cloud)
	require.Empty(t, keys)
	cloud.egraphMu.RLock()
	require.Empty(t, cloud.resultsByID, "every entry collected: the e-graph reset")
	cloud.egraphMu.RUnlock()
	requireCacheHoldersExact(t, cloud)

	a.call(callRow{key: HolderKey{engineA, 9}, session: "a3", holding: holdingOf("root")})
	a.call(callRow{key: HolderKey{engineB, 9}, session: "b3", holding: holdingOf("root")})
	require.Empty(t, a.collect())
	requireCacheHoldersExact(t, cloud)
}

// heldByHoldingsLocked is HeldBy's predicate on holdings as it was before
// the index: every holding of the entry walked.
func heldByHoldingsLocked(h *HeldBy, res *sharedResult) bool {
	for key, held := range res.holders {
		if key.Cache == h.Cache && (held.expiresAtUnix == 0 || held.expiresAtUnix > h.Now) {
			return true
		}
	}
	return false
}

// HeldBy's predicate on holdings, read from the index, answers as the walk
// over every holding did: a holding of the cache, judged by its own expiry,
// among several of that cache and others' on one entry.
func TestHeldByHoldsLikeTheWalk(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engineA, engineB := CacheID("cache-a"), CacheID("cache-b")
	at := func(expires int64) RemoteHolding {
		h := holdingOf("root")
		h.ExpiresAtUnix = expires
		return h
	}
	a.call(callRow{key: HolderKey{engineA, 1}, session: "a", holding: at(100)})
	a.call(callRow{key: HolderKey{engineA, 2}, session: "a", holding: at(300)})
	a.call(callRow{key: HolderKey{engineB, 1}, session: "b", holding: at(0)})
	require.Empty(t, a.collect())
	info, ok := cloud.RemoteEntryInfo(HolderKey{engineA, 1})
	require.True(t, ok)

	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	res := cloud.resultsByID[sharedResultID(info.Number)]
	require.Len(t, res.cacheHolders[engineA], 2, "two holdings of one cache on the entry")
	for _, tc := range []struct {
		cache CacheID
		now   int64
		held  bool
	}{
		{engineA, 50, true},   // both unexpired
		{engineA, 200, true},  // one expired, one not
		{engineA, 300, false}, // both expired
		{engineB, 1 << 40, true},
		{"cache-none", 0, false},
	} {
		h := &HeldBy{Cache: tc.cache, Now: tc.now}
		require.Equal(t, tc.held, h.holdsLocked(res), "%s at %d", tc.cache, tc.now)
		require.Equal(t, heldByHoldingsLocked(h, res), h.holdsLocked(res), "%s at %d, as the walk", tc.cache, tc.now)
	}
}

// CacheHoldings returns what EntryInfo lists of one cache's holdings, and
// EntryClassEntries the entries EquivalentEntries finds for the entry's class
// digests: the narrow reads answer as the wide ones.
func TestNarrowReadsAnswerAsTheWideOnes(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engineA, engineB := CacheID("cache-a"), CacheID("cache-b")
	f := func(input string) RemoteHolding {
		return RemoteHolding{
			Recipe:  testDigest("f-" + input),
			Request: testDigest("f-" + input),
			Field:   "f",
			Term:    &RemoteTerm{Self: testDigest("f-self"), Inputs: []digest.Digest{testDigest(input)}},
		}
	}
	a.call(callRow{key: HolderKey{engineA, 1}, session: "a", holding: f("x")})
	a.call(callRow{key: HolderKey{engineA, 2}, session: "a", holding: f("y")})
	a.call(callRow{key: HolderKey{engineA, 3}, session: "a", holding: f("y")})
	a.call(callRow{key: HolderKey{engineB, 1}, session: "b", holding: RemoteHolding{Recipe: testDigest("x"), Request: testDigest("y"), Field: "x"}})
	a.call(callRow{key: HolderKey{engineB, 2}, session: "b", holding: f("x")})
	require.Empty(t, a.collect())

	for _, key := range []HolderKey{{engineA, 1}, {engineA, 2}, {engineB, 1}} {
		info, ok := cloud.RemoteEntryInfo(key)
		require.True(t, ok)
		class, ok := cloud.EntryClassEntries(info.Number)
		require.True(t, ok)
		require.Equal(t, info.Number, class.Number)
		require.Equal(t, info.Stored, class.Stored)
		require.Equal(t, info.StoredExpiresAtUnix, class.StoredExpiresAtUnix)
		want := map[uint64]struct{}{info.Number: {}}
		for _, dig := range info.ClassDigests {
			for _, n := range cloud.EquivalentEntries(dig) {
				want[n] = struct{}{}
			}
		}
		require.Equal(t, slices.Sorted(maps.Keys(want)), class.Entries, "the entries of %v's classes", key)

		for _, cache := range []CacheID{engineA, engineB, "cache-none"} {
			var wide []string
			for _, n := range class.Entries {
				entry, ok := cloud.EntryInfo(n)
				require.True(t, ok)
				for _, h := range entry.Holdings {
					if h.Key.Cache == cache {
						wide = append(wide, fmt.Sprintf("%d:%v:%d", n, h.Key, h.ExpiresAtUnix))
					}
				}
			}
			var narrow []string
			for _, held := range cloud.CacheHoldings(cache, class.Entries) {
				require.Equal(t, cache, held.Holding.Key.Cache)
				narrow = append(narrow, fmt.Sprintf("%d:%v:%d", held.Number, held.Holding.Key, held.Holding.ExpiresAtUnix))
			}
			slices.Sort(wide)
			slices.Sort(narrow)
			require.Equal(t, wide, narrow, "%s's holdings on %v's classes", cache, key)
		}
	}
	_, ok := cloud.EntryClassEntries(1 << 40)
	require.False(t, ok)
	require.Empty(t, cloud.CacheHoldings(engineA, []uint64{1 << 40}))
}
