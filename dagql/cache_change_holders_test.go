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
	for _, part := range mRes.declaredPartsLocked() {
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

// A holding's arrival on an entry another cache holds, and its removal while
// that cache still holds it, change the holding and none of the entry's own
// facts or part answers: whether the entry stores a record turns on whether
// any engine holds it, which neither turns, and a part answer reads the
// class's stored parts, absence marks and uploads, not its holders. No reader
// keeps an entry's holders (RemoteChange), so each write names the holding
// alone. Here cache-d's holding joins R, which cache-c holds, with R's
// snapshot complete: a copy the Cloud could ask for, and its answer for R's
// snapshot stays the stored copy's. On a whole pull request's fill every
// holding that joined an entry named it, and every other engine holding the
// entry read it again at its next pass: 13.0 million reads by entry, a fifth
// of a percent of those in passes that read nothing else finding the entry's
// facts changed.
func TestChangeReportsAHoldingsArrivalAndRemovalNameItAlone(t *testing.T) {
	t.Parallel()
	h := newHolderTestClass(t)
	before, err := h.cloud.AvailablePart(h.r, h.snapshot, h.now)
	require.NoError(t, err)
	require.Equal(t, PartStored, before.State)

	rRecipe, rField := changeTestRecipe(h.cloud, h.r)
	key := HolderKey{"cache-d", 40}
	change := h.attach(t, key, RemoteHolding{Recipe: rRecipe, Request: rRecipe, Field: rField, TypeName: "Directory", ContentDigest: testDigest("holder-content"), Parts: []PersistedPartAddress{h.snapshot}, Executed: true})
	require.Equal(t, h.r, h.number(t, key), "cache-d's holding is on R")
	require.Contains(t, change.Holdings, key)
	require.NotContains(t, change.Entries, h.r, "R's facts and part answers don't change as cache-d joins it")
	require.NotContains(t, change.Entries, h.m, "nor does M's")
	after, err := h.cloud.AvailablePart(h.r, h.snapshot, h.now)
	require.NoError(t, err)
	require.Equal(t, before, after, "R's answer for its snapshot")

	collected, change, err := h.cloud.CollectRemoteHoldings(h.ctx, []HolderKey{key}, nil)
	require.NoError(t, err)
	require.Equal(t, []HolderKey{key}, collected, "cache-d's holding, which nothing owns, is collected")
	require.Contains(t, change.Holdings, key)
	require.NotContains(t, change.Entries, h.r, "R, which cache-c still holds, doesn't change as cache-d leaves it")
	facts := h.cloud.EntriesFacts([]uint64{h.r})
	require.Len(t, facts, 1, "R stays")
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

// An entry whose record marks a part absent, storing no part, answers
// "absent" for its class mates. A prune that drops its value collects it in
// the same write, its record gone first: its leaving must still name its
// class mates, whose answer turns from absent to unavailable, though the
// entry holds nothing by the time it leaves. Its engine's holding was
// collected before, so the prune is what takes it.
func TestChangeReportsAnAbsenceDonorPrunedNamesItsClassMates(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	w := newChangeWatch(t, cloud, time.Now().Unix())

	bctx, b, bsrv := transferTestCache(t)
	bsrv.InstallObject(NewClass(bsrv, ClassOpts[*inlineCaptureTestValue]{}))
	absentRes := persistedListTestResult(t, bctx, b, bsrv, "holder-absent", &inlineCaptureTestValue{})
	var absent uint64
	w.step("merge an all-absent value from cache-b", func() RemoteChange {
		change, numbers := changeTestMerge(t, ctx, cloud, "cache-b", exportTestBundle(t, bctx, b, absentRes))
		absent = numbers[uint64(absentRes.cacheSharedResult().id)]
		return change
	})
	require.NotZero(t, absent)
	absentRecipe, absentField := changeTestRecipe(cloud, absent)
	content := testDigest("holder-absent-content")
	attach := func(key HolderKey, desc RemoteHolding) func() RemoteChange {
		return func() RemoteChange {
			change, err := cloud.AttachRemoteHolding(ctx, key, desc)
			require.NoError(t, err)
			return change
		}
	}
	absentKey, mateKey := HolderKey{"cache-c", 1}, HolderKey{"cache-c", 2}
	w.step("attach cache-c's holding of the absent value", attach(absentKey, RemoteHolding{Recipe: absentRecipe, Request: absentRecipe, Field: absentField, ContentDigest: content}))
	mate := holdingOf("holder-absent-mate")
	mate.ContentDigest = content
	w.step("attach cache-c's holding of a mate, of the same content", attach(mateKey, mate))
	held, ok := cloud.RemoteHoldingInfo(mateKey)
	require.True(t, ok)
	class, ok := cloud.EntryClassEntries(held.Number)
	require.True(t, ok)
	require.Contains(t, class.Entries, absent, "the mate is in the absent value's class")
	w.step("collect cache-c's holding of the absent value", func() RemoteChange {
		change, found, err := cloud.CollectRemoteHolding(ctx, absentKey)
		require.NoError(t, err)
		require.True(t, found)
		return change
	})
	var candidates []HolderKey
	w.step("release cache-b, which merged it", func() RemoteChange {
		change := cloud.ReleaseRemoteCache(ctx, "cache-b")
		candidates = change.Candidates
		return change
	})
	w.step("collect cache-b's holdings", func() RemoteChange {
		_, change, err := cloud.CollectRemoteHoldings(ctx, candidates, nil)
		require.NoError(t, err)
		return change
	})
	cloud.egraphMu.RLock()
	kept := cloud.resultsByID[sharedResultID(absent)]
	cloud.egraphMu.RUnlock()
	require.NotNil(t, kept, "its stored record keeps the absent value with no engine's holding")
	w.step("prune every stored root", func() RemoteChange {
		report, err := cloud.PruneMetadataEstimate(ctx, 2, 1)
		require.NoError(t, err)
		require.Positive(t, report.DroppedValues)
		return report.Change
	})
	cloud.egraphMu.RLock()
	gone := cloud.resultsByID[sharedResultID(absent)] == nil
	cloud.egraphMu.RUnlock()
	require.True(t, gone, "the prune took the absent value whole")
}
