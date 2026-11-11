package dagql

import (
	"fmt"
	"maps"
	"runtime"
	"slices"
	"testing"
	"time"
	"weak"

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

// Every reader of a stored part sees the offer it was stored with: parts
// that share layers, a layer of the same blob described differently, a
// layer in a pack, and a caller that changes the offer it was given. The
// sweep's and the drops' answers are each part's own.
func TestStoredPartOffersAreAsStored(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t, WithBlobStore())
	b1, b2 := storedLayersTestLayer("b1", "the base"), storedLayersTestLayer("b2", "the base's second layer")
	b1Again := storedLayersTestLayer("b1", "the same blob, from another snapshot's history")
	b1Again.Descriptor.URLs = []string{"https://mirror.example/b1"}
	b1Again.Descriptor.Platform = &ocispecs.Platform{OS: "linux", Architecture: "arm64", OSFeatures: []string{"a"}}
	packA, packB := testDigest("pack-a"), testDigest("pack-b")
	clone := func(layers ...snapshots.ExportLayer) []snapshots.ExportLayer {
		out := make([]snapshots.ExportLayer, len(layers))
		for i, layer := range layers {
			out[i] = cloneExportLayer(layer)
		}
		return out
	}
	want := map[string]PersistedPartOffer{
		"one":   storedLayersTestPart("snapshot", clone(b1, b2, storedLayersTestLayer("t1", "")), map[digest.Digest]digest.Digest{b2.Descriptor.Digest: packA}),
		"two":   storedLayersTestPart("snapshot", clone(b1, b2, storedLayersTestLayer("t2", "")), map[digest.Digest]digest.Digest{b2.Descriptor.Digest: packA}),
		"three": storedLayersTestPart("snapshot", clone(b1Again, b2), map[digest.Digest]digest.Digest{b2.Descriptor.Digest: packB}),
		"four":  storedLayersTestPart("snapshot", clone(b1, b2), nil),
		"empty": storedLayersTestPart("snapshot", nil, nil),
	}
	numbers := map[string]uint64{}
	for _, name := range slices.Sorted(maps.Keys(want)) {
		res := storedPartTestPublish(t, ctx, c, "s", "as-stored-"+name, &captureTestValue{text: name})
		numbers[name] = uint64(res.cacheSharedResult().id)
		stored, err := c.SetStoredPart(ctx, numbers[name], want[name], 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	read := func(name string) PersistedPartOffer {
		t.Helper()
		av, err := c.AvailablePart(numbers[name], want[name].Address)
		require.NoError(t, err)
		require.Equal(t, PartStored, av.State)
		return av.Part
	}
	for name, offer := range want {
		require.Equal(t, offer, read(name), name)
	}

	// The parts share what they have in common, and nothing else.
	c.egraphMu.RLock()
	held := map[string][]*storedLayer{}
	for name, number := range numbers {
		key, err := partAddressKey(want[name].Address)
		require.NoError(t, err)
		held[name] = c.resultsByID[sharedResultID(number)].storedParts[key].layers
	}
	c.egraphMu.RUnlock()
	require.Same(t, held["one"][0], held["two"][0], "one base layer")
	require.Same(t, held["one"][1], held["two"][1], "one base layer in one pack")
	require.Same(t, held["one"][0], held["four"][0])
	require.NotSame(t, held["one"][0], held["three"][0], "the same blob, described otherwise")
	require.NotSame(t, held["one"][1], held["three"][1], "the same layer in another pack")
	require.NotSame(t, held["one"][1], held["four"][1], "the same layer in no pack")

	// A caller's changes to its offer reach no part.
	scribbled := read("one")
	scribbled.Chain.Layers[0].Description = "changed"
	scribbled.Chain.Layers[0].Descriptor.Annotations["containerd.io/uncompressed"] = "changed"
	*scribbled.Chain.Layers[0].CreatedAt = time.Unix(0, 0)
	scribbled.Chain.Packs[b2.Descriptor.Digest] = packB
	threeRead := read("three")
	threeRead.Chain.Layers[0].Descriptor.Platform.OSFeatures[0] = "changed"
	threeRead.Chain.Layers[0].Descriptor.URLs[0] = "changed"
	for name, offer := range want {
		require.Equal(t, offer, read(name), name)
	}

	blobs, err := c.LiveBlobs()
	require.NoError(t, err)
	require.ElementsMatch(t, []digest.Digest{b1.Descriptor.Digest, b2.Descriptor.Digest, testDigest("t1"), testDigest("t2")}, blobs)
	packs, err := c.LivePacks()
	require.NoError(t, err)
	require.Equal(t, slices.Sorted(slices.Values([]digest.Digest{packA, packB})), packs)
	members, err := c.LivePackMembers()
	require.NoError(t, err)
	require.Equal(t, map[digest.Digest]digest.Digest{b2.Descriptor.Digest: min(packA, packB)}, members)

	dropped, err := c.DropStoredPack(packB)
	require.NoError(t, err)
	require.Equal(t, []StoredPart{{Number: numbers["three"], Address: want["three"].Address}}, dropped)
	dropped, err = c.DropStoredParts(testDigest("t2"))
	require.NoError(t, err)
	require.Equal(t, []StoredPart{{Number: numbers["two"], Address: want["two"].Address}}, dropped)
	for _, name := range []string{"one", "four", "empty"} {
		require.Equal(t, want[name], read(name), name)
	}
}

// The cache holds a layer only while a stored part names it: once the
// parts that named a layer are gone, the collector frees it, and the table
// forgets it.
func TestStoredLayersForgetLayersNoPartNames(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t, WithBlobStore())
	res := storedPartTestPublish(t, ctx, c, "s", "forgotten-layers", &captureTestValue{text: "v"})
	number := uint64(res.cacheSharedResult().id)
	stored, err := c.SetStoredPart(ctx, number, storedLayersTestPart("snapshot", []snapshots.ExportLayer{storedLayersTestLayer("forgotten", "")}, nil), 0)
	require.NoError(t, err)
	require.True(t, stored)
	blobs := func() int {
		c.storedLayers.mu.Lock()
		defer c.storedLayers.mu.Unlock()
		return len(c.storedLayers.byBlob)
	}
	require.Equal(t, 1, blobs())

	_, err = c.DropStoredParts(testDigest("forgotten"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		runtime.GC()
		return blobs() == 0
	}, 10*time.Second, 10*time.Millisecond)
}

// A closed cache with stored parts is freed once nothing refers to it: the
// layers it holds don't keep it alive. A cache without stored parts is the
// control.
func TestClosedCacheWithStoredPartsIsFreed(t *testing.T) {
	t.Parallel()
	freed := func(store bool) func() bool {
		ctx := cacheTestContext(t.Context())
		c, err := NewCache(ctx, "", nil, nil, WithBlobStore())
		require.NoError(t, err)
		if store {
			res := storedPartTestPublish(t, ctx, c, "s", "freed-cache", &captureTestValue{text: "v"})
			stored, err := c.SetStoredPart(ctx, uint64(res.cacheSharedResult().id), storedLayersTestPart("snapshot", []snapshots.ExportLayer{storedLayersTestLayer("freed", "")}, nil), 0)
			require.NoError(t, err)
			require.True(t, stored)
		}
		require.NoError(t, c.CloseDiscardingPersistence())
		cache := weak.Make(c)
		return func() bool {
			runtime.GC()
			return cache.Value() == nil
		}
	}
	require.Eventually(t, freed(false), 10*time.Second, 10*time.Millisecond, "a closed cache without stored parts is freed")
	require.Eventually(t, freed(true), 10*time.Second, 10*time.Millisecond, "a closed cache with stored parts is freed")
}
