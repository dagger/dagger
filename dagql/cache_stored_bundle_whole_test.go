package dagql

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// heldTestHoldOver records, on the Cloud, a holding of engine cache's entry
// number on the Cloud entry cloudNumber with the cache's dependencies deps,
// as a call span would report it.
func heldTestHoldOver(t *testing.T, cloud *Cache, cache CacheID, number, cloudNumber uint64, deps []uint64) {
	t.Helper()
	entry := storedBundleTestEntry(cloud, cloudNumber)
	cloud.egraphMu.RLock()
	recipe := entry.recipeKeys[0]
	cloud.egraphMu.RUnlock()
	_, err := cloud.AttachRemoteHolding(t.Context(), HolderKey{Cache: cache, Number: number}, RemoteHolding{Recipe: recipe, Request: recipe, Field: entry.description, Deps: deps})
	require.NoError(t, err)
}

// storedBundleTestJSON is a bundle as a merge sends it.
func storedBundleTestJSON(t *testing.T, bundle ValueBundle) string {
	t.Helper()
	encoded, err := json.Marshal(bundle)
	require.NoError(t, err)
	return string(encoded)
}

// With Whole, a held row whose holding's closure names a dependency no
// holding of the recipient's cache has goes in full, and the bundle is the
// one built again with that row in Not, byte for byte. A held row whose
// closure the cache holds whole stays held, as without Whole.
func TestStoredBundleCarriesOnlyWholeHeldRows(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	now := time.Now().Unix()
	// cache-b's holding of P names its dependency 99, which cache-b holds
	// nothing of; cache-c's names none.
	heldTestHoldOver(t, cloud, "cache-b", 7, p, []uint64{99})
	heldTestHoldOver(t, cloud, "cache-c", 8, p, nil)

	got, err := cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-b", Whole: true})
	require.NoError(t, err)
	require.Empty(t, got.Held, "P isn't held whole")
	require.Equal(t, []uint64{p}, got.NotWhole)
	again, err := cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-b", Not: map[uint64]bool{p: true}})
	require.NoError(t, err)
	require.Equal(t, storedBundleTestJSON(t, again.Bundle), storedBundleTestJSON(t, got.Bundle), "the bundle built again without P held")
	require.Equal(t, again.Donors, got.Donors)

	got, err = cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-c", Whole: true})
	require.NoError(t, err)
	require.Equal(t, []uint64{p}, got.Held, "P is held whole")
	require.Empty(t, got.NotWhole)
	without, err := cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-c"})
	require.NoError(t, err)
	require.Equal(t, storedBundleTestJSON(t, without.Bundle), storedBundleTestJSON(t, got.Bundle), "as without Whole")
}
