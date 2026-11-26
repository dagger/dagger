package dagql

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// A walk of a blob-backed cache's pool counts each entry's distinct blobs in
// one slice it uses again for every entry, and the known blobs are listed
// straight from the entries' layers: neither makes a map of blobs for each
// entry, which a restore's recount of the pool did for every stored value.
// Here 400 stored values each name the same 20 layers.
//
// Not parallel: it counts the process's allocations, which other tests
// running beside it would change.
func TestThePoolsWalkMakesNoMapForEachEntry(t *testing.T) {
	ctx := cacheTestContext(context.Background())
	cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	roots := storedPersistenceTestMerge(t, ctx, cloud, storedBodyTestExportOf(t, 400))
	layers := map[string]int64{}
	for i := range 20 {
		layers[fmt.Sprintf("walk-layer-%d", i)] = int64(1000 * (i + 1))
	}
	for _, root := range roots {
		stored, _, err := cloud.SetStoredPart(ctx, root, storedPruneTestPart("snapshot", layers), 0)
		require.NoError(t, err)
		require.True(t, stored)
	}

	cloud.egraphMu.RLock()
	walked := cloud.walkPoolLocked()
	members, blobs := len(walked.members), len(walked.measure.blobs)
	walk := testing.AllocsPerRun(10, func() { cloud.walkPoolLocked() })
	cloud.egraphMu.RUnlock()
	require.Equal(t, len(layers), blobs, "the pool names each layer's blob once")
	require.GreaterOrEqual(t, members, len(roots), "every stored value is in the pool")
	t.Logf("a walk of %d entries: %.0f allocations", members, walk)
	require.Less(t, walk, float64(members)/2, "no map for each entry")

	live, err := cloud.LiveBlobs()
	require.NoError(t, err)
	require.Len(t, live, len(layers))
	listing := testing.AllocsPerRun(10, func() { _, _ = cloud.LiveBlobs() })
	t.Logf("the known blobs of %d entries: %.0f allocations", members, listing)
	require.Less(t, listing, float64(members)/2, "no map for each entry")
}
