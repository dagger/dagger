package dagql

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Holdings on a stored entry, one with an expiry of its own, answer every
// reader with the entry's stored record and its expiry beside each holding's
// own facts: RemoteHoldingInfo, CacheHoldings, EntryInfo, RemoteEntryInfo,
// EntryClassEntries, EquivalentHolders and HolderClosure. The test logs a
// digest of every answer, so that two builds of the cache can be shown to
// answer alike.
func TestStoredEntryHoldingAnswers(t *testing.T) {
	t.Parallel()
	ctx, cloud, root, dep := storedBundleTestCloud(t, "root")
	// A fixed expiry, not the clock's, so that two builds' digests compare.
	const expiry = int64(4_000_000_000)
	entry := storedBundleTestEntry(cloud, root)
	cloud.egraphMu.Lock()
	entry.expiresAtUnix = expiry
	cloud.egraphMu.Unlock()

	info, ok := cloud.EntryInfo(root)
	require.True(t, ok)
	require.True(t, info.Stored)
	require.Equal(t, expiry, info.StoredExpiresAtUnix)
	depInfo, ok := cloud.EntryInfo(dep)
	require.True(t, ok)

	sender := HolderKey{Cache: "cache-a", Number: bundleSenderNumberForTest(t, cloud, root)}
	depKey := HolderKey{Cache: "cache-b", Number: 3}
	rootKey := HolderKey{Cache: "cache-b", Number: 5}
	other := HolderKey{Cache: "cache-b", Number: 9}
	_, err := cloud.AttachRemoteHolding(ctx, depKey, RemoteHolding{Recipe: depInfo.Recipes[0], Field: "bundle-dep", TypeName: "transferTestValue"})
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(ctx, rootKey, RemoteHolding{
		Recipe:        info.Recipes[0],
		Field:         "bundle-root",
		TypeName:      "transferTestValue",
		ContentDigest: testDigest("root-content"),
		ExpiresAtUnix: expiry + 100,
		Deps:          []uint64{depKey.Number, 4},
		Parts:         []PersistedPartAddress{{Part: "snapshot"}, {Part: "fs"}},
		Replacements:  2,
		Executed:      true,
	})
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(ctx, other, RemoteHolding{Recipe: info.Recipes[0], Field: "bundle-root", TypeName: "transferTestValue"})
	require.NoError(t, err)
	for _, key := range []HolderKey{depKey, rootKey, other} {
		_, err := cloud.AddRemoteHold(ctx, key, "session-b")
		require.NoError(t, err)
	}
	_, err = cloud.AddRemoteHold(ctx, rootKey, "session-a")
	require.NoError(t, err)
	_, err = cloud.ObserveRemoteRetention(ctx, rootKey, *retainedAt(1, 1, expiry+200))
	require.NoError(t, err)
	_, _, err = cloud.ApplyHeldValueState(ctx, rootKey, HeldValueState{Replacements: 2, OfferedParts: []PersistedPartAddress{{Part: "metadata"}}})
	require.NoError(t, err)

	held, ok := cloud.RemoteHoldingInfo(rootKey)
	require.True(t, ok)
	require.Equal(t, RemoteHeldEntry{
		Number:              root,
		Stored:              true,
		StoredExpiresAtUnix: expiry,
		Recipes:             info.Recipes,
		Holding: RemoteHoldingInfo{
			Key:                    rootKey,
			TypeName:               "transferTestValue",
			ContentDigest:          testDigest("root-content"),
			ExpiresAtUnix:          expiry + 100,
			Retained:               true,
			RetentionExpiresAtUnix: expiry + 200,
			Sessions:               []string{"session-a", "session-b"},
			Deps:                   []uint64{depKey.Number},
			UnknownDeps:            []uint64{4},
			Parts:                  []PersistedPartAddress{{Part: "fs"}, {Part: "snapshot"}},
			OfferedParts:           []PersistedPartAddress{{Part: "metadata"}},
			Replacements:           2,
			Executed:               true,
		},
	}, held)

	byCache := cloud.CacheHoldings("cache-b", []uint64{root, dep})
	require.Len(t, byCache, 3)
	for _, h := range byCache {
		if h.Number == root {
			require.True(t, h.Stored)
			require.Equal(t, expiry, h.StoredExpiresAtUnix)
		}
	}
	require.Equal(t, []HolderKey{rootKey, other}, []HolderKey{byCache[0].Holding.Key, byCache[1].Holding.Key}, "an entry's holdings of one cache, by number")
	require.Equal(t, held, byCache[0])

	entryInfo, ok := cloud.EntryInfo(root)
	require.True(t, ok)
	require.True(t, entryInfo.Stored)
	require.Equal(t, expiry, entryInfo.StoredExpiresAtUnix)
	var keys []HolderKey
	for _, h := range entryInfo.Holdings {
		keys = append(keys, h.Key)
	}
	require.Equal(t, []HolderKey{sender, rootKey, other}, keys, "every holding on the entry, by key")
	byHolding, ok := cloud.RemoteEntryInfo(rootKey)
	require.True(t, ok)
	require.Equal(t, entryInfo, byHolding)

	// Every answer a dump of the replay prints, for each holding and entry,
	// digested for comparing builds.
	answers := map[string]any{"entry": entryInfo, "dep": mustEntryInfo(t, cloud, dep), "byCache": byCache}
	class, ok := cloud.EntryClassEntries(root)
	require.True(t, ok)
	require.True(t, class.Stored)
	require.Equal(t, expiry, class.StoredExpiresAtUnix)
	answers["class"] = class
	equivalent := map[string][]HolderKey{}
	for _, dig := range entryInfo.ClassDigests {
		equivalent[dig] = cloud.EquivalentHolders(dig)
	}
	answers["equivalent"] = equivalent
	for _, key := range []HolderKey{sender, depKey, rootKey, other} {
		h, ok := cloud.RemoteHoldingInfo(key)
		require.True(t, ok)
		answers[string(key.Cache)+"/"+jsonString(t, key.Number)] = map[string]any{"held": h, "closure": cloud.HolderClosure(key)}
	}
	require.Equal(t, []HolderKey{depKey, rootKey}, cloud.HolderClosure(rootKey))
	data, err := json.Marshal(answers)
	require.NoError(t, err)
	t.Logf("answers digest %x", sha256.Sum256(data))
}

func mustEntryInfo(t *testing.T, cloud *Cache, number uint64) RemoteEntryInfo {
	t.Helper()
	info, ok := cloud.EntryInfo(number)
	require.True(t, ok)
	return info
}

func jsonString(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}
