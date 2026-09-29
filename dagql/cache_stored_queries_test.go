package dagql

import (
	"context"
	"testing"

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
		stored, err := cloud.SetStoredPart(ctx, first, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		got, err := cloud.AvailablePart(first, address)
		require.NoError(t, err)
		require.Equal(t, PartStored, got.State)
		require.Equal(t, first, got.Donor)
		require.Equal(t, testLiveOffer().Value, got.Part.Value)
	})
	t.Run("its own, expired", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, first, _ := storedQueriesTestClass(t)
		stored, err := cloud.SetStoredPart(ctx, first, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		expire(cloud, first)
		got, err := cloud.AvailablePart(first, address)
		require.NoError(t, err)
		require.Equal(t, PartUnavailable, got.State, "a stored part lasts only while its value does")
	})
	t.Run("an unexpired class member's", func(t *testing.T) {
		t.Parallel()
		ctx, cloud, first, second := storedQueriesTestClass(t)
		stored, err := cloud.SetStoredPart(ctx, second, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		got, err := cloud.AvailablePart(first, address)
		require.NoError(t, err)
		require.Equal(t, PartStored, got.State)
		require.Equal(t, second, got.Donor, "the donor is named")

		expire(cloud, second)
		got, err = cloud.AvailablePart(first, address)
		require.NoError(t, err)
		require.Equal(t, PartUnavailable, got.State, "an expired member's part is not available")
	})
	t.Run("an unknown entry", func(t *testing.T) {
		t.Parallel()
		_, cloud, _, _ := storedQueriesTestClass(t)
		_, err := cloud.AvailablePart(12345, address)
		require.ErrorIs(t, err, ErrUnknownEntry)
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
	for key, id := range cloud.holderEntries {
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
	_, err := c.AvailablePart(1, PersistedPartAddress{Part: "snapshot"})
	require.ErrorContains(t, err, "no blob store")

	ctx, cloud, first, second := storedQueriesTestClass(t)
	part := testLiveOffer()
	part.Value.Services = []TransferredServiceBinding{{ServiceResultID: second, Hostname: "svc"}}
	part.Owner.DependencyIDs = []uint64{second}
	stored, err := cloud.SetStoredPart(ctx, first, part, 0)
	require.NoError(t, err)
	require.True(t, stored)
	got, err := cloud.AvailablePart(first, PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	got.Part.Value.Services[0].Hostname = "changed"
	got.Part.Owner.DependencyIDs[0] = 0
	again, err := cloud.AvailablePart(first, PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	require.Equal(t, "svc", again.Part.Value.Services[0].Hostname, "the stored part is unchanged")
	require.Equal(t, []uint64{second}, again.Part.Owner.DependencyIDs)
}
