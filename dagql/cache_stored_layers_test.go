package dagql

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// storedLayersTestLayer is a layer as an engine's export describes one: its
// descriptor's annotations, its history's description and creation time.
func storedLayersTestLayer(name, description string) snapshots.ExportLayer {
	created := time.Unix(1_760_000_000, 0).UTC()
	return snapshots.ExportLayer{
		Descriptor: ocispecs.Descriptor{
			MediaType: ocispecs.MediaTypeImageLayerGzip,
			Digest:    testDigest(name),
			Size:      int64(len(name)) << 10,
			Annotations: map[string]string{
				"containerd.io/uncompressed": testDigest("uncompressed-" + name).String(),
				"buildkit/createdat":         created.Format(time.RFC3339Nano),
			},
		},
		Description: description,
		CreatedAt:   &created,
	}
}

// storedLayersTestPart is a part whose chain is the layers, with the packs.
func storedLayersTestPart(part string, layers []snapshots.ExportLayer, packs map[digest.Digest]digest.Digest) PersistedPartOffer {
	return PersistedPartOffer{
		Address: PersistedPartAddress{Part: PartKey(part)},
		Value:   SnapshotValue{Kind: "directory", Path: "/"},
		Chain:   OfferedChain{Layers: layers, Packs: packs},
	}
}

// Parts that sit on one base hold its layers once: storing many parts on a
// deep base grows the live heap by far less than one copy of the base for
// each part. A copy of a layer, with its annotations, takes about 500 bytes,
// and a part's pointer to a held layer 8.
//
// Not parallel: it measures the process's live heap, which other tests
// running beside it would change.
func TestStoredPartsHoldSharedLayersOnce(t *testing.T) {
	const parts, baseLayers = 200, 40
	ctx, c := storedPartTestCache(t, WithBlobStore())
	base := make([]snapshots.ExportLayer, baseLayers)
	for i := range base {
		base[i] = storedLayersTestLayer(fmt.Sprintf("shared-base-%d", i), fmt.Sprintf("layer %d of the base", i))
	}
	numbers := make([]uint64, parts)
	offers := make([]PersistedPartOffer, parts)
	for i := range parts {
		res := storedPartTestPublish(t, ctx, c, "s", fmt.Sprintf("shared-base-part-%d", i), &captureTestValue{text: "v"})
		numbers[i] = uint64(res.cacheSharedResult().id)
		chain := make([]snapshots.ExportLayer, 0, baseLayers+1)
		for _, layer := range base {
			// Each part's chain is decoded apart, as an export's are.
			chain = append(chain, cloneExportLayer(layer))
		}
		chain = append(chain, storedLayersTestLayer(fmt.Sprintf("top-%d", i), "the part's own layer"))
		offers[i] = storedLayersTestPart("snapshot", chain, nil)
	}

	before := storedLayersTestLiveHeap()
	for i := range parts {
		stored, err := c.SetStoredPart(ctx, numbers[i], offers[i], 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	grown := storedLayersTestLiveHeap() - before
	runtime.KeepAlive(offers)

	perReference := float64(grown) / float64(parts*baseLayers)
	require.Less(t, perReference, 64.0, "storing %d parts on a base of %d layers grew the live heap by %d bytes, %.0f bytes for each part's reference to a base layer", parts, baseLayers, grown, perReference)
}

func storedLayersTestLiveHeap() int64 {
	var stats runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&stats)
	return int64(stats.HeapAlloc)
}
