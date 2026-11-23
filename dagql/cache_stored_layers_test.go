package dagql

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
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
		stored, _, err := c.SetStoredPart(ctx, numbers[i], offers[i], 0)
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
		stored, _, err := c.SetStoredPart(ctx, numbers[name], want[name], 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	read := func(name string) PersistedPartOffer {
		t.Helper()
		return storedPartTestOffer(t, c, numbers[name], want[name].Address)
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

	dropped, _, err := c.DropStoredPack(packB)
	require.NoError(t, err)
	require.Equal(t, []StoredPart{{Number: numbers["three"], Address: want["three"].Address}}, dropped)
	dropped, _, err = c.DropStoredParts(testDigest("t2"))
	require.NoError(t, err)
	require.Equal(t, []StoredPart{{Number: numbers["two"], Address: want["two"].Address}}, dropped)
	for _, name := range []string{"one", "four", "empty"} {
		require.Equal(t, want[name], read(name), name)
	}
}

// A layer is found among the many the table holds for its blob as fast as
// among one. Engines describe one blob many ways: each build of the same
// content stamps its own creation time, and an export notes whether it
// computed the blob. On a service that several pull requests had filled, an
// upload's parts were compared against every such variant of each layer's
// blob, one by one under the table's one lock: storing an upload's parts took
// up to 11 s at a stopping engine's last export. The timing is each arm's
// fastest of several rounds, so a busy machine slows both alike.
func TestStoredLayersFindALayerAmongManyOfItsBlobAsAmongOne(t *testing.T) {
	t.Parallel()
	const variants, lookups, rounds = 2000, 200, 5
	variant := func(i int) snapshots.ExportLayer {
		layer := storedLayersTestLayer("many-variants", "the same blob, built again")
		created := time.Unix(1_760_000_000+int64(i), 0).UTC()
		layer.CreatedAt = &created
		layer.Descriptor.Annotations["buildkit/createdat"] = created.Format(time.RFC3339Nano)
		return layer
	}
	part := func(table *storedLayers, layer snapshots.ExportLayer) storedPart {
		return table.part(storedLayersTestPart("snapshot", []snapshots.ExportLayer{layer}, nil))
	}
	one, many := &storedLayers{}, &storedLayers{}
	held := []storedPart{part(one, variant(0))}
	for i := range variants {
		held = append(held, part(many, variant(i)))
	}
	// Each variant is held apart, and a lookup finds the one it equals.
	require.NotSame(t, held[1].layers[0], held[2].layers[0])
	require.Same(t, held[len(held)-1].layers[0], part(many, variant(variants-1)).layers[0])
	require.Same(t, held[1].layers[0], part(many, variant(0)).layers[0])

	fastest := func(table *storedLayers, layer snapshots.ExportLayer) time.Duration {
		best := time.Duration(math.MaxInt64)
		for range rounds {
			start := time.Now()
			for range lookups {
				part(table, cloneExportLayer(layer))
			}
			best = min(best, time.Since(start))
		}
		return best
	}
	// The last variant held: a lookup that compares one by one compares it
	// last.
	amongOne := fastest(one, variant(0))
	amongMany := fastest(many, variant(variants-1))
	runtime.KeepAlive(held)
	require.Less(t, amongMany, 10*amongOne, "%d lookups among %d variants of a blob took %s, against %s among one", lookups, variants, amongMany, amongOne)
}

// The table holds two layers as one exactly when reflect.DeepEqual calls them
// equal, as it did when it compared every layer of a blob. Layers equal but
// built apart are one: their annotations added in another order, their
// creation time in a zone made again, copies, and two decodes of one
// encoding. Layers DeepEqual tells apart stay apart, those that encode alike
// and so share a key among them: a nil against an empty map, slice or byte
// string.
func TestStoredLayersHoldEqualLayersAsOneAndOthersApart(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 11, 23, 7, 33, 58, 899_000_000, time.FixedZone("PST", -8*3600))
	base := func() snapshots.ExportLayer {
		at := created
		return snapshots.ExportLayer{
			Descriptor: ocispecs.Descriptor{
				MediaType: ocispecs.MediaTypeImageLayerGzip,
				Digest:    testDigest("equal-or-apart"),
				Size:      4 << 20,
				URLs:      []string{"https://mirror.example/a", "https://mirror.example/b"},
				Data:      []byte("inline"),
				Platform:  &ocispecs.Platform{OS: "linux", Architecture: "arm64", OSFeatures: []string{"x"}},
			},
			Description: "the same blob",
			CreatedAt:   &at,
			Computed:    true,
		}
	}
	annotations := map[string]string{}
	for i := range 64 {
		annotations[fmt.Sprintf("annotation-%02d", i)] = fmt.Sprintf("value %d", i)
	}
	// withAnnotations adds the annotations in the order of keys.
	withAnnotations := func(layer snapshots.ExportLayer, keys []string) snapshots.ExportLayer {
		layer.Descriptor.Annotations = map[string]string{}
		for _, k := range keys {
			layer.Descriptor.Annotations[k] = annotations[k]
		}
		return layer
	}
	keys := slices.Sorted(maps.Keys(annotations))
	reversed := slices.Clone(keys)
	slices.Reverse(reversed)

	equal := map[string]func() snapshots.ExportLayer{
		"annotations added in another order": func() snapshots.ExportLayer { return withAnnotations(base(), reversed) },
		"a creation time in a zone made again": func() snapshots.ExportLayer {
			layer := withAnnotations(base(), keys)
			at := created.In(time.FixedZone("PST", -8*3600))
			layer.CreatedAt = &at
			return layer
		},
		"a copy": func() snapshots.ExportLayer { return cloneExportLayer(withAnnotations(base(), keys)) },
	}
	apart := map[string]func() snapshots.ExportLayer{
		"no annotations": func() snapshots.ExportLayer {
			layer := withAnnotations(base(), nil)
			layer.Descriptor.Annotations = nil
			return layer
		},
		"empty annotations": func() snapshots.ExportLayer { return withAnnotations(base(), nil) },
		"no URLs": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.Descriptor.URLs = nil
			return l
		},
		"empty URLs": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.Descriptor.URLs = []string{}
			return l
		},
		"no data": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.Descriptor.Data = nil
			return l
		},
		"empty data": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.Descriptor.Data = []byte{}
			return l
		},
		"no platform": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.Descriptor.Platform = nil
			return l
		},
		"an empty platform": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.Descriptor.Platform = &ocispecs.Platform{}
			return l
		},
		"no OS features": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.Descriptor.Platform = &ocispecs.Platform{OS: "linux", Architecture: "arm64"}
			return l
		},
		"empty OS features": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.Descriptor.Platform = &ocispecs.Platform{OS: "linux", Architecture: "arm64", OSFeatures: []string{}}
			return l
		},
		"no creation time": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.CreatedAt = nil
			return l
		},
		"the same instant in UTC": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			at := created.UTC()
			l.CreatedAt = &at
			return l
		},
		"not computed": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.Computed = false
			return l
		},
		"another history": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.Description = "rebuilt"
			return l
		},
		"another descriptor": func() snapshots.ExportLayer {
			l := withAnnotations(base(), keys)
			l.Descriptor.Size++
			return l
		},
	}
	part := func(table *storedLayers, layer snapshots.ExportLayer, pack digest.Digest) *storedLayer {
		var packs map[digest.Digest]digest.Digest
		if pack != "" {
			packs = map[digest.Digest]digest.Digest{layer.Descriptor.Digest: pack}
		}
		return table.part(storedLayersTestPart("snapshot", []snapshots.ExportLayer{layer}, packs)).layers[0]
	}
	first := withAnnotations(base(), keys)
	for name, build := range equal {
		require.True(t, reflect.DeepEqual(first, build()), name)
		table := &storedLayers{}
		held := part(table, cloneExportLayer(first), "")
		require.Same(t, held, part(table, build(), ""), name)
	}
	for name, build := range apart {
		require.False(t, reflect.DeepEqual(first, build()), name)
		table := &storedLayers{}
		held := part(table, cloneExportLayer(first), "")
		other := part(table, build(), "")
		require.NotSame(t, held, other, name)
		require.Equal(t, build(), other.layer, name)
		require.Same(t, held, part(table, cloneExportLayer(first), ""), name)
		require.Same(t, other, part(table, build(), ""), name)
	}

	// Two decodes of one encoding, as two engines' exports of one layer
	// reach the service, are one layer.
	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	decode := func() snapshots.ExportLayer {
		var layer snapshots.ExportLayer
		require.NoError(t, json.Unmarshal(encoded, &layer))
		return layer
	}
	require.True(t, reflect.DeepEqual(decode(), decode()))
	decoded := &storedLayers{}
	require.Same(t, part(decoded, decode(), ""), part(decoded, decode(), ""))

	// Twins encode alike, and so share a key, but DeepEqual tells them
	// apart: each is held apart, and each is found again.
	twins := map[string][2]func(*snapshots.ExportLayer){
		"annotations": {
			func(l *snapshots.ExportLayer) { l.Descriptor.Annotations = nil },
			func(l *snapshots.ExportLayer) { l.Descriptor.Annotations = map[string]string{} },
		},
		"URLs": {
			func(l *snapshots.ExportLayer) { l.Descriptor.URLs = nil },
			func(l *snapshots.ExportLayer) { l.Descriptor.URLs = []string{} },
		},
		"data": {
			func(l *snapshots.ExportLayer) { l.Descriptor.Data = nil },
			func(l *snapshots.ExportLayer) { l.Descriptor.Data = []byte{} },
		},
		"OS features": {
			func(l *snapshots.ExportLayer) { l.Descriptor.Platform.OSFeatures = nil },
			func(l *snapshots.ExportLayer) { l.Descriptor.Platform.OSFeatures = []string{} },
		},
	}
	for name, twin := range twins {
		build := func(i int) snapshots.ExportLayer {
			l := cloneExportLayer(first)
			twin[i](&l)
			return l
		}
		require.Equal(t, storedLayerEncoding(build(0), ""), storedLayerEncoding(build(1), ""), name)
		require.False(t, reflect.DeepEqual(build(0), build(1)), name)
		table := &storedLayers{}
		nilOne, emptyOne := part(table, build(0), ""), part(table, build(1), "")
		require.NotSame(t, nilOne, emptyOne, name)
		require.True(t, reflect.DeepEqual(build(0), nilOne.layer), name)
		require.True(t, reflect.DeepEqual(build(1), emptyOne.layer), name)
		require.Same(t, nilOne, part(table, build(0), ""), name)
		require.Same(t, emptyOne, part(table, build(1), ""), name)
	}
	// The same layer in another pack, or in none, is held apart.
	table := &storedLayers{}
	inA := part(table, cloneExportLayer(first), testDigest("pack-a"))
	require.NotSame(t, inA, part(table, cloneExportLayer(first), testDigest("pack-b")))
	require.NotSame(t, inA, part(table, cloneExportLayer(first), ""))
	require.Same(t, inA, part(table, cloneExportLayer(first), testDigest("pack-a")))
}

// The cache holds a layer only while a stored part names it: once the
// parts that named a layer are gone, the collector frees it, and the table
// forgets it.
func TestStoredLayersForgetLayersNoPartNames(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t, WithBlobStore())
	res := storedPartTestPublish(t, ctx, c, "s", "forgotten-layers", &captureTestValue{text: "v"})
	number := uint64(res.cacheSharedResult().id)
	stored, _, err := c.SetStoredPart(ctx, number, storedLayersTestPart("snapshot", []snapshots.ExportLayer{storedLayersTestLayer("forgotten", "")}, nil), 0)
	require.NoError(t, err)
	require.True(t, stored)
	layers := func() int {
		c.storedLayers.mu.Lock()
		defer c.storedLayers.mu.Unlock()
		return len(c.storedLayers.byKey)
	}
	require.Equal(t, 1, layers())

	_, _, err = c.DropStoredParts(testDigest("forgotten"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		runtime.GC()
		return layers() == 0
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
			stored, _, err := c.SetStoredPart(ctx, uint64(res.cacheSharedResult().id), storedLayersTestPart("snapshot", []snapshots.ExportLayer{storedLayersTestLayer("freed", "")}, nil), 0)
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
