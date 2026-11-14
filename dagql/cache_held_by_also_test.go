package dagql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
	also := func(number uint64) bool { return number == p }

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
