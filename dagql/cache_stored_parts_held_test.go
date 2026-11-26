package dagql

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/stretchr/testify/require"
)

// An entry holds its stored parts in little beside the parts themselves: a
// Cloud whose 400 entries store a part each, on one layer they share, frees
// under 384 bytes a part when its entries let go of their parts. Held in a
// map, each part took a group of slots and a copy of its own apart, about
// 500 bytes.
//
// Not parallel: it measures the process's live heap, which other tests
// running beside it would change.
func TestStoredPartsHoldLittleBesideTheirParts(t *testing.T) {
	const parts = 400
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	layer := storedLayersTestLayer("held-parts-base", "the base every part is on")
	var entries []*sharedResult
	for i := range parts {
		res := storedPartTestPublish(t, ctx, cloud, "s", fmt.Sprintf("held-part-%d", i), &captureTestValue{text: fmt.Sprint(i)})
		part := PersistedPartOffer{
			Address: PersistedPartAddress{Part: "snapshot"},
			Value:   SnapshotValue{Kind: "directory", Path: "/"},
			Chain:   OfferedChain{Layers: []snapshots.ExportLayer{layer}},
		}
		stored, _, err := cloud.SetStoredPart(ctx, uint64(res.cacheSharedResult().id), part, 0)
		require.NoError(t, err)
		require.True(t, stored)
		entries = append(entries, res.cacheSharedResult())
	}
	cloud.egraphMu.Lock()
	defer cloud.egraphMu.Unlock()
	before := storedLayersTestLiveHeap()
	for _, res := range entries {
		cloud.changeStoredPartsLocked(res, func() { res.storedParts = nil })
	}
	freed := before - storedLayersTestLiveHeap()
	runtime.KeepAlive(entries)
	perPart := float64(freed) / float64(parts)
	t.Logf("letting go of %d stored parts freed %d bytes, %.0f a part", parts, freed, perPart)
	require.Less(t, perPart, 384.0, "each part took %.0f bytes", perPart)
}
