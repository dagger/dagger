package dagql

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// requireHeldEntryMatches checks that RemoteHoldingInfo reports, for every
// key given and every holding the cache has, what RemoteEntryInfo reports for
// that holding and its entry, and that the two report the same absence. It
// returns the descriptions by key.
func requireHeldEntryMatches(t *testing.T, cloud *Cache, step string, keys []HolderKey) map[HolderKey]RemoteHeldEntry {
	t.Helper()
	held, _ := cloudIndexes(cloud)
	all := slices.Concat(keys, held)
	slices.SortFunc(all, compareHolderKeys)
	all = slices.Compact(all)
	out := map[HolderKey]RemoteHeldEntry{}
	for _, key := range all {
		info, ok := cloud.RemoteEntryInfo(key)
		got, gotOK := cloud.RemoteHoldingInfo(key)
		require.Equal(t, ok, gotOK, "%s: %v exists in both reads or neither", step, key)
		if !ok {
			require.Equal(t, RemoteHeldEntry{}, got, "%s: %v", step, key)
			continue
		}
		want := RemoteHeldEntry{
			Number:              info.Number,
			Stored:              info.Stored,
			StoredExpiresAtUnix: info.StoredExpiresAtUnix,
			Recipes:             info.Recipes,
		}
		found := false
		for _, h := range info.Holdings {
			if h.Key == key {
				want.Holding, found = h, true
			}
		}
		require.True(t, found, "%s: the entry lists %v", step, key)
		require.Equal(t, want, got, "%s: %v", step, key)
		out[key] = got
	}
	return out
}

// RemoteHoldingInfo describes one holding and its entry's own facts exactly
// as RemoteEntryInfo does, whatever happened to the holding, its entry and
// the entry's other holdings: holdings of many caches on a stored entry and
// on one known only through holdings, offered parts, a replaced value state,
// an expired copy, retention and record, a record replaced in place, ended
// sessions and a released cache before and after collection, and a prune
// that drops the value.
func TestRemoteHoldingInfoMatchesEntryInfo(t *testing.T) {
	t.Parallel()
	bundle, recipe := mergeTestSource(t, "root", "value")
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	merged, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	number := merged.Imported()[0].ResultID
	sender := HolderKey{Cache: "cache-a", Number: bundleSenderNumberForTest(t, cloud, number)}
	a := newCloudApplier(t, cloud)
	keys := []HolderKey{sender, {Cache: "cache-none", Number: 1}}

	got := requireHeldEntryMatches(t, cloud, "stored", keys)
	require.True(t, got[sender].Stored)
	require.Equal(t, number, got[sender].Number)

	// Engines hold the stored entry by its recipe, each with a dependency it
	// holds, on an entry known only through holdings, and one it doesn't.
	engines := []CacheID{"cache-b", "cache-c", "cache-d", "cache-e"}
	var roots []HolderKey
	for i, engine := range engines {
		dep, root := HolderKey{Cache: engine, Number: 1}, HolderKey{Cache: engine, Number: 10}
		a.call(callRow{key: dep, session: "s-" + string(engine), holding: holdingOf("dep")})
		a.call(callRow{
			key:     root,
			session: "s-" + string(engine),
			holding: RemoteHolding{
				Recipe:        recipe,
				Request:       testDigest(fmt.Sprintf("request-%d", i)),
				Field:         "root",
				Term:          &RemoteTerm{Self: testDigest("root-self"), Inputs: []digest.Digest{testDigest("input")}},
				ContentDigest: testDigest("content"),
				TypeName:      "Container",
				ExpiresAtUnix: time.Now().Add(time.Hour).Unix(),
				Deps:          []uint64{dep.Number, 99},
				Parts:         []PersistedPartAddress{{Part: "fs"}, {Part: "metadata"}},
				Executed:      i%2 == 0,
			},
			retained: retainedAt(1, 10, time.Now().Add(time.Hour).Unix()),
		})
		require.NoError(t, cloud.AddRemoteHold(ctx, root, "s2-"+string(engine)))
		roots = append(roots, root)
		keys = append(keys, dep, root)
	}
	require.Empty(t, a.collect())
	got = requireHeldEntryMatches(t, cloud, "held", keys)
	require.Equal(t, number, got[roots[0]].Number, "on the stored entry")
	require.False(t, got[HolderKey{Cache: "cache-b", Number: 1}].Stored, "known only through holdings")
	require.Equal(t, []uint64{99}, got[roots[0]].Holding.UnknownDeps)
	require.Len(t, got[roots[0]].Holding.Sessions, 2)

	// An offered part, at the same count.
	_, ok, err := cloud.ApplyHeldValueState(ctx, roots[0], HeldValueState{OfferedParts: []PersistedPartAddress{{Part: "snapshot"}}})
	require.NoError(t, err)
	require.True(t, ok)
	got = requireHeldEntryMatches(t, cloud, "offered", keys)
	require.Equal(t, []PersistedPartAddress{{Part: "snapshot"}}, got[roots[0]].Holding.OfferedParts)

	// A replaced value state: new parts, dependencies, content and an expiry
	// already past.
	_, ok, err = cloud.ApplyHeldValueState(ctx, roots[1], HeldValueState{
		Replacements:  1,
		Deps:          []uint64{1},
		Parts:         []PersistedPartAddress{{Part: "metadata"}},
		ExpiresAtUnix: time.Now().Add(-time.Hour).Unix(),
		ContentDigest: testDigest("replaced"),
	})
	require.NoError(t, err)
	require.True(t, ok)
	got = requireHeldEntryMatches(t, cloud, "replaced", keys)
	require.Equal(t, uint64(1), got[roots[1]].Holding.Replacements)
	require.Empty(t, got[roots[1]].Holding.UnknownDeps)
	require.Equal(t, testDigest("replaced"), got[roots[1]].Holding.ContentDigest)
	require.Less(t, got[roots[1]].Holding.ExpiresAtUnix, time.Now().Unix(), "an expired copy is still a holding")

	// Retention that has expired, before and after collection: still held,
	// by its sessions.
	a.observe(roots[2], *retainedAt(2, 20, time.Now().Add(-time.Hour).Unix()))
	got = requireHeldEntryMatches(t, cloud, "retention expired", keys)
	require.True(t, got[roots[2]].Holding.Retained)
	require.Empty(t, a.collect())
	got = requireHeldEntryMatches(t, cloud, "retention expired, collected", keys)
	require.Contains(t, got, roots[2])

	// The stored record expired, then replaced in place by the same export.
	entry := storedBundleTestEntry(cloud, number)
	mergeTestExpire(cloud, entry)
	expired := requireHeldEntryMatches(t, cloud, "record expired", keys)
	require.Less(t, expired[sender].StoredExpiresAtUnix, time.Now().Unix())
	require.True(t, expired[sender].Stored)
	again, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	require.Equal(t, number, again.Imported()[0].ResultID, "replaced in place")
	require.Equal(t, uint64(1), again.Values[0].Replacements, "the record was replaced")
	got = requireHeldEntryMatches(t, cloud, "record replaced", keys)
	require.True(t, got[sender].Stored)
	require.NotEqual(t, expired[sender].StoredExpiresAtUnix, got[sender].StoredExpiresAtUnix, "the replaced record's own expiry")

	// Ended sessions and a dropped retention release cache-b's holdings, and
	// cache-c's cache is released whole: each is still described, with no
	// owner, until collection removes it.
	a.endSession("cache-b", "s-cache-b")
	a.endSession("cache-b", "s2-cache-b")
	a.observe(roots[0], droppedAt(3, 30))
	got = requireHeldEntryMatches(t, cloud, "sessions ended", keys)
	require.Empty(t, got[roots[0]].Holding.Sessions)
	require.False(t, got[roots[0]].Holding.Retained)
	require.Contains(t, got, HolderKey{Cache: "cache-b", Number: 1})
	require.NotEmpty(t, a.collect())
	got = requireHeldEntryMatches(t, cloud, "sessions ended, collected", keys)
	require.NotContains(t, got, roots[0])
	require.NotContains(t, got, HolderKey{Cache: "cache-b", Number: 1})

	a.candidates = append(a.candidates, cloud.ReleaseRemoteCache(ctx, "cache-c")...)
	got = requireHeldEntryMatches(t, cloud, "cache released", keys)
	require.Empty(t, got[roots[1]].Holding.Sessions)
	require.False(t, got[roots[1]].Holding.Retained)
	require.Contains(t, got, HolderKey{Cache: "cache-c", Number: 1})
	require.NotEmpty(t, a.collect())
	got = requireHeldEntryMatches(t, cloud, "cache released, collected", keys)
	require.NotContains(t, got, roots[1])
	require.NotContains(t, got, HolderKey{Cache: "cache-c", Number: 1})
	require.Contains(t, got, roots[2])
	require.True(t, got[roots[2]].Holding.Executed)

	// A prune drops the stored value: the entry stores no record, and no
	// holding stays marked executed.
	_, removed, _, err := cloud.removePrunedEdge(ctx, sharedResultID(number))
	require.NoError(t, err)
	require.True(t, removed)
	got = requireHeldEntryMatches(t, cloud, "pruned", keys)
	require.False(t, got[roots[2]].Stored)
	require.False(t, got[roots[2]].Holding.Executed)
}
