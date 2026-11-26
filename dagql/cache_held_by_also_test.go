package dagql

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// heldWhateverChanged is a HeldBy.Also that names numbers held whatever has
// changed of them since: at the largest position.
func heldWhateverChanged(numbers ...uint64) func(uint64) (uint64, bool) {
	return func(number uint64) (uint64, bool) { return math.MaxUint64, slices.Contains(numbers, number) }
}

// heldByAlsoTestHeld returns the numbers of a bundle's held rows.
func heldByAlsoTestHeld(bundle ValueBundle) map[uint64]bool {
	held := map[uint64]bool{}
	for _, value := range bundle.Values {
		if value.Held != "" {
			held[value.SenderNumber] = true
		}
	}
	return held
}

// A recipient holds an entry it has no holding of when Also says so, as an
// engine holds the records of a file it restored: the bundle carries the
// entry as a held row, and prices it as one. An entry in Not, which the
// recipient answered stale, or whose value has expired, goes in full.
func TestHeldByAlsoHoldsWhatItNames(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	now := time.Now().Unix()
	also := heldWhateverChanged(p)

	none, err := cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh"})
	require.NoError(t, err)
	require.Empty(t, heldByAlsoTestHeld(none.Bundle), "no holding, nothing held")

	got, err := cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh", Also: also})
	require.NoError(t, err)
	require.Equal(t, map[uint64]bool{p: true}, heldByAlsoTestHeld(got.Bundle), "the dependency Also names goes held, the root in full")
	sizes, err := cloud.StoredSizes(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh", Also: also})
	require.NoError(t, err)
	whole, err := cloud.StoredSizes(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh"})
	require.NoError(t, err)
	require.Less(t, sizes[0].Bytes, whole[0].Bytes, "priced as a held row")

	stale, err := cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh", Also: also, Not: map[uint64]bool{p: true}})
	require.NoError(t, err)
	require.Empty(t, heldByAlsoTestHeld(stale.Bundle), "answered stale, it goes in full")

	mergeTestExpire(cloud, storedBundleTestEntry(cloud, p))
	expired, err := cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh", Also: also})
	require.NoError(t, err)
	require.Empty(t, heldByAlsoTestHeld(expired.Bundle), "an expired value isn't held")
}

// A recipient holds what Also names as of the position Also gives with it,
// as an engine holds a file's records as the file's build carried them: the
// entry goes held while no write since that position changed what a bundle
// carries of it (ChangedSince). Once a write has, it goes in full, priced in
// full, and the bundle names it Changed; as of a position at or after the
// write, it is held again.
func TestHeldByAlsoHoldsAsOfItsPosition(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	now := time.Now().Unix()
	asOf := func(position uint64) func(uint64) (uint64, bool) {
		return func(number uint64) (uint64, bool) { return position, number == p }
	}
	built := cloud.BundlePosition()
	got, err := cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh", Also: asOf(built)})
	require.NoError(t, err)
	require.Equal(t, map[uint64]bool{p: true}, heldByAlsoTestHeld(got.Bundle), "unchanged since, it goes held")
	require.Empty(t, got.Changed)

	stored, _, err := cloud.SetStoredPart(ctx, p, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	require.True(t, cloud.ChangedSince([]uint64{p}, built)[0], "a part stored on it changes what a bundle carries of it")

	changed, err := cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh", Also: asOf(built)})
	require.NoError(t, err)
	require.Empty(t, heldByAlsoTestHeld(changed.Bundle), "changed since the position Also gives, it goes in full")
	require.Equal(t, []uint64{p}, changed.Changed)
	sizes, err := cloud.StoredSizes(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh", Also: asOf(built)})
	require.NoError(t, err)
	whole, err := cloud.StoredSizes(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh"})
	require.NoError(t, err)
	require.Equal(t, whole[0].Bytes, sizes[0].Bytes, "priced in full")

	again, err := cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh", Also: asOf(cloud.BundlePosition())})
	require.NoError(t, err)
	require.Equal(t, map[uint64]bool{p: true}, heldByAlsoTestHeld(again.Bundle), "as of a position after the write, it is held")
	require.Empty(t, again.Changed)
}
