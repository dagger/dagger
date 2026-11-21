package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// holderTestClass is a Cloud cache with one class of two entries: R, merged
// from cache-a, whose snapshot is stored, a holder of that part; and M, merged
// from cache-b with no part stored and no part marked absent, a member that
// holds nothing. cache-c's holdings of both, of one content digest, join them.
type holderTestClass struct {
	ctx      context.Context
	cloud    *Cache
	now      int64
	r, m     uint64
	mRecipe  RemoteHolding
	snapshot PersistedPartAddress
}

func newHolderTestClass(t *testing.T) *holderTestClass {
	t.Helper()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	h := &holderTestClass{ctx: ctx, cloud: cloud, now: time.Now().Unix(), snapshot: PersistedPartAddress{Part: "snapshot"}}

	actx, a, asrv := transferTestCache(t)
	rRes := persistedListTestResult(t, actx, a, asrv, "holder-root", &transferTestValue{Text: "root"})
	change, numbers := changeTestMerge(t, ctx, cloud, "cache-a", exportTestBundle(t, actx, a, rRes))
	require.NotEmpty(t, change.Entries)
	h.r = numbers[uint64(rRes.cacheSharedResult().id)]
	require.NotZero(t, h.r)

	memberBundle, _ := mergeTestSource(t, "holder-member", "member")
	reply, err := cloud.MergeValues(ctx, "cache-b", memberBundle)
	require.NoError(t, err)
	h.m = reply.Imported()[0].ResultID

	rRecipe, rField := changeTestRecipe(cloud, h.r)
	mRecipe, mField := changeTestRecipe(cloud, h.m)
	content := testDigest("holder-content")
	h.mRecipe = RemoteHolding{Recipe: mRecipe, Request: mRecipe, Field: mField, TypeName: "Directory", ContentDigest: content}
	h.attach(t, HolderKey{"cache-c", 10}, RemoteHolding{Recipe: rRecipe, Request: rRecipe, Field: rField, TypeName: "Directory", ContentDigest: content, Parts: []PersistedPartAddress{h.snapshot}, Executed: true})
	h.attach(t, HolderKey{"cache-c", 11}, h.mRecipe)
	stored, _, err := cloud.SetStoredPart(ctx, h.r, storedPruneTestPart("snapshot", map[string]int64{"holder-r-layer": 10}), 0)
	require.NoError(t, err)
	require.True(t, stored)

	class, ok := cloud.EntryClassEntries(h.r)
	require.True(t, ok)
	require.Contains(t, class.Entries, h.m, "M joined R's class")
	// M holds nothing: no part stored, none its record marks absent.
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	mRes := cloud.resultsByID[sharedResultID(h.m)]
	require.Empty(t, mRes.storedParts)
	declared, err := mRes.declaredPartsLocked()
	require.NoError(t, err)
	for _, part := range declared {
		require.NotEqual(t, "absent", part.state, "M marks %s absent", part.key)
	}
	return h
}

func (h *holderTestClass) attach(t *testing.T, key HolderKey, desc RemoteHolding) RemoteChange {
	t.Helper()
	change, err := h.cloud.AttachRemoteHolding(h.ctx, key, desc)
	require.NoError(t, err)
	return change
}

func (h *holderTestClass) number(t *testing.T, key HolderKey) uint64 {
	t.Helper()
	held, ok := h.cloud.RemoteHoldingInfo(key)
	require.True(t, ok)
	return held.Number
}

// A new entry that holds no part, joining a class, changes no class mate's
// part answer, and no reader keeps a class's list of entries: its write names
// it alone, not the class's holder or its other members. On a docs-only edit
// such joins named a class's every entry at every new result, and the
// service's passes asked each one's part answers again.
func TestChangeReportsAJoinOfANonHolderNamesItAlone(t *testing.T) {
	t.Parallel()
	h := newHolderTestClass(t)
	key := HolderKey{"cache-c", 30}
	change := h.attach(t, key, RemoteHolding{Recipe: testDigest("holder-x"), Request: testDigest("holder-x"), Field: "holder-x", TypeName: "Directory", ContentDigest: testDigest("holder-content")})
	x := h.number(t, key)
	class, ok := h.cloud.EntryClassEntries(h.r)
	require.True(t, ok)
	require.Contains(t, class.Entries, x, "X joined R's class")
	require.Contains(t, change.Entries, x)
	require.NotContains(t, change.Entries, h.r, "R's part answers don't change")
	require.NotContains(t, change.Entries, h.m, "M's part answers don't change")
}

// A change to a member that holds no part, here a kept call that lowers M's
// expiry, changes no class mate's part answer: its write names M, not R.
func TestChangeReportsANonHoldersChangeNamesItAlone(t *testing.T) {
	t.Parallel()
	h := newHolderTestClass(t)
	desc := h.mRecipe
	desc.Request = testDigest("holder-m-call")
	desc.ExpiresAtUnix = h.now + 600
	change := h.attach(t, HolderKey{"cache-c", 11}, desc)
	require.Contains(t, change.Entries, h.m)
	require.NotContains(t, change.Entries, h.r, "R's part answers don't change")
}

// A part stored on one member changes every class mate's answer for it: its
// write names them all.
func TestChangeReportsAHoldersChangeNamesItsClassMates(t *testing.T) {
	t.Parallel()
	h := newHolderTestClass(t)
	stored, change, err := h.cloud.SetStoredPart(h.ctx, h.r, storedPruneTestPart("mount:/cache", map[string]int64{"holder-r-mount": 10}), 0)
	require.NoError(t, err)
	require.True(t, stored)
	require.Contains(t, change.Entries, h.r)
	require.Contains(t, change.Entries, h.m, "M can now borrow R's mount")
}

// Only a holder answers for a part: a member that stores no copy of it and
// whose record doesn't mark it absent never answers, and its expiry doesn't
// bound the answer. M expiring before R doesn't change what R's class answers
// for R's snapshot, so the answer holds as long as R does.
func TestAvailablePartAnswersFromHoldersAlone(t *testing.T) {
	t.Parallel()
	h := newHolderTestClass(t)
	desc := h.mRecipe
	desc.Request = testDigest("holder-m-call")
	desc.ExpiresAtUnix = h.now + 600
	h.attach(t, HolderKey{"cache-c", 11}, desc)

	h.cloud.egraphMu.RLock()
	rExpires := h.cloud.resultsByID[sharedResultID(h.r)].expiresAtUnix
	mExpires := h.cloud.resultsByID[sharedResultID(h.m)].expiresAtUnix
	h.cloud.egraphMu.RUnlock()
	require.Equal(t, h.now+600, mExpires)
	require.True(t, rExpires == 0 || rExpires > mExpires, "R outlives M: %d against %d", rExpires, mExpires)

	for _, of := range []uint64{h.r, h.m} {
		av, err := h.cloud.AvailablePart(of, h.snapshot, h.now)
		require.NoError(t, err)
		require.Equal(t, PartStored, av.State)
		require.Equal(t, h.r, av.Donor)
		require.Equal(t, rExpires, av.ValidUntilUnix, "the answer for entry %d holds while its holder R does", of)
	}
}
