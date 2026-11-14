package dagql

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// holderEntriesForTest returns the entry of every holding, by key, from the
// engine caches' indexes. Requires egraphMu.
func (c *Cache) holderEntriesForTest() map[HolderKey]sharedResultID {
	out := map[HolderKey]sharedResultID{}
	for cache, state := range c.remoteCaches {
		for number, id := range state.entries {
			out[HolderKey{Cache: cache, Number: number}] = id
		}
	}
	return out
}

// requireCacheHoldersExact checks every entry's holdings by engine cache
// (cacheHolderRefsLocked) against a recomputation from all its holdings:
// each engine cache's holding numbers, the Cloud's own holding left out, and
// that the engine caches' indexes name exactly the holdings on entries.
func requireCacheHoldersExact(t *testing.T, c *Cache) {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	indexed := map[HolderKey]sharedResultID{}
	for id, entry := range c.resultsByID {
		refs := entry.holderRefsLocked()
		require.True(t, slices.IsSortedFunc(refs, func(a, b holderRef) int { return compareHolderKeys(a.key, b.key) }), "entry %d", id)
		want := map[CacheID]map[uint64]struct{}{}
		for _, ref := range refs {
			if senderCache(ref.key.Cache) {
				continue
			}
			indexed[ref.key] = id
			if want[ref.key.Cache] == nil {
				want[ref.key.Cache] = map[uint64]struct{}{}
			}
			want[ref.key.Cache][ref.key.Number] = struct{}{}
		}
		got := map[CacheID]map[uint64]struct{}{}
		for cache := range want {
			for _, ref := range entry.cacheHolderRefsLocked(cache) {
				if got[cache] == nil {
					got[cache] = map[uint64]struct{}{}
				}
				got[cache][ref.key.Number] = struct{}{}
			}
		}
		require.Equal(t, want, got, "entry %d", id)
	}
	require.Equal(t, indexed, c.holderEntriesForTest())
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

	a.candidates = append(a.candidates, cloud.ReleaseRemoteCache(t.Context(), engineA).Candidates...)
	a.collect()
	requireCacheHoldersExact(t, cloud)

	for _, cache := range []CacheID{engineB, engineC} {
		a.candidates = append(a.candidates, cloud.ReleaseRemoteCache(t.Context(), cache).Candidates...)
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
func heldByHoldingsLocked(h *HeldBy, res *sharedResult, nowUnix int64) bool {
	for _, ref := range res.holderRefsLocked() {
		if key, held := ref.key, ref.h; key.Cache == h.Cache && (held.expiresAtUnix == 0 || held.expiresAtUnix > nowUnix) {
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
	require.Len(t, res.cacheHolderRefsLocked(engineA), 2, "two holdings of one cache on the entry")
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
		h := &HeldBy{Cache: tc.cache}
		require.Equal(t, tc.held, h.holdsLocked(res, tc.now), "%s at %d", tc.cache, tc.now)
		require.Equal(t, heldByHoldingsLocked(h, res, tc.now), h.holdsLocked(res, tc.now), "%s at %d, as the walk", tc.cache, tc.now)
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

// EntriesHeld returns what EntryInfo reports of each entry's own facts, and
// the keys of its first holdings in EntryInfo's order, skipping a number
// the cache has no entry for.
func TestEntriesHeldAnswersAsEntryInfo(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	f := RemoteHolding{Recipe: testDigest("f"), Request: testDigest("f"), Field: "f"}
	g := RemoteHolding{Recipe: testDigest("g"), Request: testDigest("g"), Field: "g"}
	for i, cache := range []CacheID{"cache-d", "cache-b", "cache-e", "cache-a", "cache-c"} {
		a.call(callRow{key: HolderKey{cache, uint64(i + 1)}, session: string(cache), holding: f})
	}
	a.call(callRow{key: HolderKey{"cache-a", 9}, session: "cache-a", holding: f})
	a.call(callRow{key: HolderKey{"cache-a", 10}, session: "cache-a", holding: g})
	require.Empty(t, a.collect())

	fInfo, ok := cloud.RemoteEntryInfo(HolderKey{"cache-a", 4})
	require.True(t, ok)
	gInfo, ok := cloud.RemoteEntryInfo(HolderKey{"cache-a", 10})
	require.True(t, ok)
	require.Len(t, fInfo.Holdings, 6)
	numbers := []uint64{gInfo.Number, 1 << 40, fInfo.Number}
	for _, limit := range []int{0, 1, 3, 6, 10} {
		held := cloud.EntriesHeld(numbers, limit)
		require.Len(t, held, 2, "the missing number is skipped")
		for i, info := range []RemoteEntryInfo{gInfo, fInfo} {
			require.Equal(t, info.Number, held[i].Number)
			require.Equal(t, info.Stored, held[i].Stored)
			require.Equal(t, info.StoredExpiresAtUnix, held[i].StoredExpiresAtUnix)
			var want []HolderKey
			for _, h := range info.Holdings[:min(limit, len(info.Holdings))] {
				want = append(want, h.Key)
			}
			require.Equal(t, want, held[i].Keys, "entry %d's first %d holdings", info.Number, limit)
		}
	}
}

// EntriesFacts answers, for each entry, the entry's own facts as
// RemoteHoldingInfo reports them for every holding on it, recipes
// included, and skips a number with no entry.
func TestEntriesFactsAnswersAsRemoteHoldingInfo(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	f := RemoteHolding{Recipe: testDigest("f"), Request: testDigest("f"), Field: "f"}
	g := RemoteHolding{Recipe: testDigest("g"), Request: testDigest("g-other"), Field: "g"}
	a.call(callRow{key: HolderKey{"cache-a", 1}, session: "cache-a", holding: f})
	a.call(callRow{key: HolderKey{"cache-b", 2}, session: "cache-b", holding: f})
	a.call(callRow{key: HolderKey{"cache-a", 3}, session: "cache-a", holding: g})
	require.Empty(t, a.collect())

	keys := []HolderKey{{"cache-a", 1}, {"cache-b", 2}, {"cache-a", 3}}
	held := map[HolderKey]RemoteHeldEntry{}
	for _, key := range keys {
		h, ok := cloud.RemoteHoldingInfo(key)
		require.True(t, ok)
		held[key] = h
	}
	numbers := []uint64{held[keys[2]].Number, 1 << 40, held[keys[0]].Number}
	facts := cloud.EntriesFacts(numbers)
	require.Len(t, facts, 2, "the missing number is skipped")
	for i, key := range []HolderKey{keys[2], keys[0]} {
		want := held[key]
		require.Equal(t, EntryFacts{Number: want.Number, Stored: want.Stored, StoredExpiresAtUnix: want.StoredExpiresAtUnix, Recipes: want.Recipes}, facts[i])
	}
	require.Equal(t, held[keys[0]].Number, held[keys[1]].Number, "both engines' f are one entry")
}

// EntryRecipe names, for an entry, a recipe its engine holdings attach by:
// a holding attached by it for a cache that had none lands on that entry, in
// the cache's own read and in a hold's. It reports false for a number the
// cache has no entry for.
func TestEntryRecipeAttachesAHoldingToItsEntry(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	engineA, engineB, engineC := CacheID("cache-a"), CacheID("cache-b"), CacheID("cache-c")
	desc := RemoteHolding{Recipe: testDigest("recipe-r"), Request: testDigest("request-r"), Field: "r"}
	a.call(callRow{key: HolderKey{engineA, 7}, session: "a", holding: desc})
	info, ok := cloud.RemoteEntryInfo(HolderKey{engineA, 7})
	require.True(t, ok)

	recipe, ok := cloud.EntryRecipe(info.Number)
	require.True(t, ok)
	_, err := cloud.AttachRemoteHolding(ctx, HolderKey{engineB, 3}, RemoteHolding{Recipe: recipe, Field: "r"})
	require.NoError(t, err)
	number, ok := cloud.HolderEntry(HolderKey{engineB, 3})
	require.True(t, ok)
	require.Equal(t, info.Number, number, "a holding attached by the entry's recipe lands on the entry")

	w := cloud.HoldRemoteWrites()
	held, ok := w.EntryRecipe(info.Number)
	require.True(t, ok)
	require.Equal(t, recipe, held, "a hold answers as the cache")
	_, err = w.AttachRemoteHolding(ctx, HolderKey{engineC, 4}, RemoteHolding{Recipe: held, Field: "r"})
	require.NoError(t, err)
	number, ok = w.HolderEntry(HolderKey{engineC, 4})
	require.True(t, ok)
	require.Equal(t, info.Number, number)
	_, ok = w.EntryRecipe(1 << 40)
	require.False(t, ok)
	require.NoError(t, w.Release(ctx))

	_, ok = cloud.EntryRecipe(1 << 40)
	require.False(t, ok, "no entry, no recipe")
}
