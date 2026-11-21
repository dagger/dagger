package dagql

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// storedQueriesTestClass merges two records of different recipes into a new
// Cloud cache and joins them in one class by a content digest. It returns
// the cache and the two entries' numbers.
func storedQueriesTestClass(t *testing.T) (context.Context, *Cache, uint64, uint64) {
	t.Helper()
	first, _ := mergeTestSource(t, "class-first", "first")
	second, _ := mergeTestSource(t, "class-second", "second")
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	var numbers []uint64
	for _, bundle := range []ValueBundle{first, second} {
		reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
		require.NoError(t, err)
		number := reply.Imported()[0].ResultID
		cloud.egraphMu.RLock()
		res := cloud.resultsByID[sharedResultID(number)]
		cloud.egraphMu.RUnlock()
		require.NoError(t, cloud.TeachContentDigest(ctx, Result[Typed]{shared: res}, testDigest("same-content")))
		numbers = append(numbers, number)
	}
	return ctx, cloud, numbers[0], numbers[1]
}

// EquivalentEntries names every entry of a digest's class, by number, and
// nothing for a digest no class has.
func TestEquivalentEntries(t *testing.T) {
	t.Parallel()
	_, cloud, first, second := storedQueriesTestClass(t)
	require.Equal(t, []uint64{first, second}, cloud.EquivalentEntries(testDigest("same-content").String()))
	require.Nil(t, cloud.EquivalentEntries(testDigest("no-such-digest").String()))
}

// TermEntries finds an entry by its term where its recipe is unknown: the
// same operation over an input another digest names, once the two are one
// class, as the structural lookup finds it. An input no class has, or a term
// no entry has, finds nothing.
func TestTermEntries(t *testing.T) {
	t.Parallel()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	run := func(frame *ResultCall, v int) (uint64, *CacheDecision) {
		t.Helper()
		req := &CallRequest{ResultCall: frame, CacheEvidence: NewCacheDecision()}
		res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(frame, v), nil
		})
		require.NoError(t, err)
		number, err := c.PersistedResultID(res)
		require.NoError(t, err)
		return number, req.CacheEvidence
	}
	overInput := func(src digest.Digest) *ResultCall {
		frame := cacheTestIntCall("term-entries-op")
		frame.Args = []*ResultCallArg{{
			Name: "src",
			Value: &ResultCallLiteral{
				Kind:                 ResultCallLiteralKindDigestedString,
				DigestedStringValue:  "witnessed",
				DigestedStringDigest: src,
			},
		}}
		return frame
	}
	digA, digB := digest.FromString("term-entries-input-a"), digest.FromString("term-entries-input-b")
	number, ev := run(overInput(digA), 1)
	require.Equal(t, []digest.Digest{digA}, ev.StructuralInputs)

	require.Nil(t, c.TermEntries(ev.SelfDigest, []digest.Digest{digB}), "digB is in no class yet")
	run(cacheTestIntCall("term-entries-teacher",
		call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: digA},
		call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: digB},
	), 2)

	require.Nil(t, c.EquivalentEntries(cacheTestCallDigest(overInput(digB)).String()), "the recipe over digB is unknown")
	require.Equal(t, []uint64{number}, c.TermEntries(ev.SelfDigest, []digest.Digest{digB}))
	require.Equal(t, []uint64{number}, c.TermEntries(ev.SelfDigest, []digest.Digest{digA}))
	require.Nil(t, c.TermEntries(digest.FromString("another operation"), []digest.Digest{digB}))
	require.Nil(t, c.TermEntries(ev.SelfDigest, []digest.Digest{digest.FromString("unknown input")}))
}

// A stored part is available while the value it belongs to is: the entry's
// own while its value is unexpired, or else an unexpired class member's,
// named as the donor. An expired member's part is not.
func TestAvailablePartStored(t *testing.T) {
	t.Parallel()
	address := PersistedPartAddress{Part: "snapshot"}
	expire := func(c *Cache, number uint64) {
		c.egraphMu.RLock()
		res := c.resultsByID[sharedResultID(number)]
		c.egraphMu.RUnlock()
		mergeTestExpire(c, res)
	}
	t.Run("its own, unexpired", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, first, _ := storedQueriesTestClass(t)
		stored, _, err := cloud.SetStoredPart(ctx, first, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		got, err := cloud.AvailablePart(first, address, time.Now().Unix())
		require.NoError(t, err)
		require.Equal(t, PartStored, got.State)
		require.Equal(t, first, got.Donor)
		require.Equal(t, testLiveOffer().Value, got.Part.Value)
	})
	t.Run("its own, expired", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, first, _ := storedQueriesTestClass(t)
		stored, _, err := cloud.SetStoredPart(ctx, first, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		expire(cloud, first)
		got, err := cloud.AvailablePart(first, address, time.Now().Unix())
		require.NoError(t, err)
		require.Equal(t, PartUnavailable, got.State, "a stored part lasts only while its value does")
	})
	t.Run("an unexpired class member's", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, first, second := storedQueriesTestClass(t)
		stored, _, err := cloud.SetStoredPart(ctx, second, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		got, err := cloud.AvailablePart(first, address, time.Now().Unix())
		require.NoError(t, err)
		require.Equal(t, PartStored, got.State)
		require.Equal(t, second, got.Donor, "the donor is named")

		expire(cloud, second)
		got, err = cloud.AvailablePart(first, address, time.Now().Unix())
		require.NoError(t, err)
		require.Equal(t, PartUnavailable, got.State, "an expired member's part is not available")
	})
	t.Run("an unknown entry", func(t *testing.T) {
		t.Parallel()
		_, cloud, _, _ := storedQueriesTestClass(t)
		_, err := cloud.AvailablePart(12345, address, time.Now().Unix())
		require.ErrorIs(t, err, ErrUnknownEntry)
	})
}

// AvailablePart answers at the reader's clock, not its own: a reader that
// judged an entry's expiry at one moment and asks of its parts later, or that
// replays a recorded run, is told what the cache held at its moment. Each
// value here expires an hour from the wall clock, so only the reader's clock
// can have passed it.
func TestAvailablePartAtTheReadersClock(t *testing.T) {
	t.Parallel()
	address := PersistedPartAddress{Part: "snapshot"}
	expiresAt := time.Now().Add(time.Hour).Unix()
	expireAt := func(c *Cache, number uint64) {
		c.egraphMu.Lock()
		c.resultsByID[sharedResultID(number)].expiresAtUnix = expiresAt
		c.egraphMu.Unlock()
	}
	t.Run("its own part", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, first, _ := storedQueriesTestClass(t)
		stored, _, err := cloud.SetStoredPart(ctx, first, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		expireAt(cloud, first)
		got, err := cloud.AvailablePart(first, address, expiresAt-1)
		require.NoError(t, err)
		require.Equal(t, PartStored, got.State, "the value hasn't expired at the reader's moment")
		got, err = cloud.AvailablePart(first, address, expiresAt)
		require.NoError(t, err)
		require.Equal(t, PartUnavailable, got.State, "the value has expired at the reader's moment, though not by the wall clock")
	})
	t.Run("a class member's part", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, first, second := storedQueriesTestClass(t)
		stored, _, err := cloud.SetStoredPart(ctx, second, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		expireAt(cloud, second)
		got, err := cloud.AvailablePart(first, address, expiresAt-1)
		require.NoError(t, err)
		require.Equal(t, PartStored, got.State)
		require.Equal(t, second, got.Donor)
		got, err = cloud.AvailablePart(first, address, expiresAt)
		require.NoError(t, err)
		require.Equal(t, PartUnavailable, got.State, "the donor has expired at the reader's moment")
	})
	t.Run("a value expired by the wall clock", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, first, _ := storedQueriesTestClass(t)
		stored, _, err := cloud.SetStoredPart(ctx, first, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		past := time.Now().Add(-time.Hour)
		cloud.egraphMu.Lock()
		cloud.resultsByID[sharedResultID(first)].expiresAtUnix = past.Unix()
		cloud.egraphMu.Unlock()
		got, err := cloud.AvailablePart(first, address, past.Add(-time.Minute).Unix())
		require.NoError(t, err)
		require.Equal(t, PartStored, got.State, "a replay's reader, before the expiry, is told the part was there")
	})
}

// EntryInfo describes a Cloud entry by its number, as RemoteEntryInfo does
// from a holding: the number, whether it stores a record, and that record's
// expiry. An entry known only through holdings stores none; an unknown
// number has no entry.
func TestEntryInfo(t *testing.T) {
	t.Parallel()
	ctx, cloud, first, _ := storedQueriesTestClass(t)
	entry := storedBundleTestEntry(cloud, first)
	mergeTestExpire(cloud, entry)
	cloud.egraphMu.RLock()
	expiry := entry.expiresAtUnix
	cloud.egraphMu.RUnlock()
	info, ok := cloud.EntryInfo(first)
	require.True(t, ok)
	require.Equal(t, first, info.Number)
	require.True(t, info.Stored)
	require.Equal(t, expiry, info.StoredExpiresAtUnix, "the stored record's own expiry")
	require.Contains(t, info.ClassDigests, testDigest("same-content").String())
	byKey, ok := cloud.RemoteEntryInfo(HolderKey{Cache: "cache-a", Number: bundleSenderNumberForTest(t, cloud, first)})
	require.True(t, ok)
	require.Equal(t, info.Number, byKey.Number, "the holding's entry")

	key := HolderKey{Cache: "cache-x", Number: 7}
	_, err := cloud.AttachRemoteHolding(ctx, key, holdingOf("holdings-only"))
	require.NoError(t, err)
	holdingsOnly, ok := cloud.RemoteEntryInfo(key)
	require.True(t, ok)
	require.NotZero(t, holdingsOnly.Number)
	require.False(t, holdingsOnly.Stored)
	require.Zero(t, holdingsOnly.StoredExpiresAtUnix)
	byNumber, ok := cloud.EntryInfo(holdingsOnly.Number)
	require.True(t, ok)
	require.Equal(t, holdingsOnly, byNumber)

	_, ok = cloud.EntryInfo(12345)
	require.False(t, ok)
}

// bundleSenderNumberForTest returns the number of cache-a's holding on the
// Cloud entry numbered number.
func bundleSenderNumberForTest(t *testing.T, cloud *Cache, number uint64) uint64 {
	t.Helper()
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	for key, id := range cloud.holderEntriesForTest() {
		if key.Cache == "cache-a" && uint64(id) == number {
			return key.Number
		}
	}
	t.Fatalf("no cache-a holding on %d", number)
	return 0
}

// AvailablePart answers only on a blob-backed cache, and its part is a copy:
// changing it leaves the stored part as it was.
func TestAvailablePartCopiesAndNeedsABlobStore(t *testing.T) {
	t.Parallel()
	_, c, _ := transferTestCache(t)
	_, err := c.AvailablePart(1, PersistedPartAddress{Part: "snapshot"}, time.Now().Unix())
	require.ErrorContains(t, err, "no blob store")

	ctx, cloud, first, second := storedQueriesTestClass(t)
	part := testLiveOffer()
	part.Value.Services = []TransferredServiceBinding{{ServiceResultID: second, Hostname: "svc"}}
	part.Owner.DependencyIDs = []uint64{second}
	stored, _, err := cloud.SetStoredPart(ctx, first, part, 0)
	require.NoError(t, err)
	require.True(t, stored)
	got, err := cloud.AvailablePart(first, PersistedPartAddress{Part: "snapshot"}, time.Now().Unix())
	require.NoError(t, err)
	got.Part.Value.Services[0].Hostname = "changed"
	got.Part.Owner.DependencyIDs[0] = 0
	again, err := cloud.AvailablePart(first, PersistedPartAddress{Part: "snapshot"}, time.Now().Unix())
	require.NoError(t, err)
	require.Equal(t, "svc", again.Part.Value.Services[0].Hostname, "the stored part is unchanged")
	require.Equal(t, []uint64{second}, again.Part.Owner.DependencyIDs)
}

// AvailableParts answers each query as AvailablePart does, in the queries'
// order, an error for the query that has one among answers for the others:
// a stored part of a class member, one not stored, an unknown entry, and an
// address with no part.
func TestAvailablePartsAnswerAsAvailablePart(t *testing.T) {
	t.Parallel()
	ctx, cloud, first, second := storedQueriesTestClass(t)
	stored, _, err := cloud.SetStoredPart(ctx, second, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	now := time.Now().Unix()
	queries := []PartQuery{
		{Number: first, Address: PersistedPartAddress{Part: "snapshot"}},
		{Number: second, Address: PersistedPartAddress{Part: "snapshot"}},
		{Number: first, Address: PersistedPartAddress{Part: "fs"}},
		{Number: 12345, Address: PersistedPartAddress{Part: "snapshot"}},
		{Number: first, Address: PersistedPartAddress{}},
	}
	got := cloud.AvailableParts(queries, now)
	require.Len(t, got, len(queries))
	for i, q := range queries {
		want, wantErr := cloud.AvailablePart(q.Number, q.Address, now)
		if wantErr != nil {
			require.Error(t, got[i].Err, "query %d", i)
			require.Equal(t, wantErr.Error(), got[i].Err.Error(), "query %d", i)
			continue
		}
		require.NoError(t, got[i].Err, "query %d", i)
		require.Equal(t, want, got[i].PartAvailability, "query %d", i)
	}
	require.Equal(t, PartStored, got[0].State, "the class member's stored part")
	require.ErrorIs(t, got[3].Err, ErrUnknownEntry)
}

// RemoteHoldingInfos answers each key as RemoteHoldingInfo does, in the
// keys' order, with ok false for a holding the cache doesn't have.
func TestRemoteHoldingInfosAnswerAsRemoteHoldingInfo(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	a, b, missing := HolderKey{"cache-c", 1}, HolderKey{"cache-c", 2}, HolderKey{"cache-c", 3}
	for key, field := range map[HolderKey]string{a: "infos-a", b: "infos-b"} {
		_, err := cloud.AttachRemoteHolding(ctx, key, RemoteHolding{Recipe: testDigest(field), Request: testDigest(field), Field: field, TypeName: "Directory", ContentDigest: testDigest(field)})
		require.NoError(t, err)
	}
	keys := []HolderKey{b, missing, a}
	held, ok := cloud.RemoteHoldingInfos(keys)
	require.Len(t, held, len(keys))
	require.Len(t, ok, len(keys))
	for i, key := range keys {
		want, wantOK := cloud.RemoteHoldingInfo(key)
		require.Equal(t, wantOK, ok[i], "key %v", key)
		require.Equal(t, want, held[i], "key %v", key)
	}
	require.Equal(t, []bool{true, false, true}, ok)
}

// A reader that asks only what the cache has of a part pays nothing for the
// part's contents: the answer for a stored part of 64 layers costs what the
// answer for one of a single layer does. The layers are as an engine's
// export describes them, with annotations and a creation time, which a copy
// of the part would copy.
//
// Not parallel: it measures the process's allocations, which tests running
// beside it would add to.
func TestAvailablePartStateCopiesNothing(t *testing.T) {
	ctx, c := storedPartTestCache(t, WithBlobStore())
	answerBytes := func(layers int) uint64 {
		t.Helper()
		chain := make([]snapshots.ExportLayer, layers)
		for i := range chain {
			chain[i] = storedLayersTestLayer(fmt.Sprintf("state-only-%d-%d", layers, i), "a layer of the part")
		}
		res := storedPartTestPublish(t, ctx, c, "s", fmt.Sprintf("state-only-%d", layers), &captureTestValue{text: "v"})
		number := uint64(res.cacheSharedResult().id)
		part := storedLayersTestPart("snapshot", chain, nil)
		stored, _, err := c.SetStoredPart(ctx, number, part, 0)
		require.NoError(t, err)
		require.True(t, stored)
		now := time.Now().Unix()
		answered := true
		bytes := allocatedBytesPerRun(100, func() {
			av, err := c.AvailablePart(number, part.Address, now)
			answered = answered && err == nil && av.State == PartStored && av.Donor == number
		})
		require.True(t, answered, "every answer is the entry's own stored part")
		return bytes
	}
	one, many := answerBytes(1), answerBytes(64)
	t.Logf("an answer for a stored part allocates %d bytes with 1 layer, %d with 64", one, many)
	require.Less(t, many, one+512, "an answer that reads only the state grew with the part's layers")
}
