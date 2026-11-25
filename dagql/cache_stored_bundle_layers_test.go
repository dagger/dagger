package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// storedLayersBundleCloud returns a blob-backed cache holding one record for
// each of chains, each storing a part whose chain is that chain, and the
// records' numbers, in order. The records are exported from an engine's
// cache and merged, as the service receives them, and each chain is a copy
// of its own, as an export's chains are decoded apart.
func storedLayersBundleCloud(t *testing.T, chains [][]snapshots.ExportLayer, packs []map[digest.Digest]digest.Digest) (context.Context, *Cache, []uint64) {
	t.Helper()
	actx := cacheTestContext(context.Background())
	a, err := NewCache(actx, filepath.Join(t.TempDir(), "a.db"), nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, a.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*storedBenchValue]{}))
	actx = srvToContext(ContextWithCache(actx, a), srv)
	var roots []AnyResult
	for i := range chains {
		value := &storedBenchValue{Text: fmt.Sprintf("layers-%d", i)}
		frame := &ResultCall{Kind: ResultCallKindField, Field: value.Text, Type: NewResultCallType(value.Type())}
		res, err := a.GetOrInitCall(actx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame)
		})
		require.NoError(t, err)
		roots = append(roots, res)
	}
	var bundle ValueBundle
	require.NoError(t, a.WithExportedValues(actx, ValueSelection{Roots: roots}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))

	ctx := cacheTestContext(context.Background())
	cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	numbers := map[TransferOrdinal]uint64{}
	for _, value := range reply.Values {
		numbers[value.Ordinal] = value.Number
	}
	var out []uint64
	for i, root := range bundle.Roots {
		number := numbers[root.Ordinal]
		chain := make([]snapshots.ExportLayer, len(chains[i]))
		for j, layer := range chains[i] {
			chain[j] = cloneExportLayer(layer)
		}
		var partPacks map[digest.Digest]digest.Digest
		if packs != nil {
			partPacks = packs[i]
		}
		stored, _, err := cloud.SetStoredPart(ctx, number, storedLayersTestPart("snapshot", chain, partPacks), 0)
		require.NoError(t, err)
		require.True(t, stored)
		out = append(out, number)
	}
	return ctx, cloud, out
}

// storedLayersBundleWire is the bundle as it goes on the wire: its JSON with
// each layer listed once (ShareChainLayers).
func storedLayersBundleWire(t *testing.T, bundle ValueBundle) string {
	t.Helper()
	shared, err := ShareChainLayers(bundle)
	require.NoError(t, err)
	data, err := json.Marshal(shared)
	require.NoError(t, err)
	return string(data)
}

// storedLayersBundleFull is the bundle with each output's chain carrying its
// layers, their packs and their addresses, as an engine that merges it gives
// them back to the chains (ResolveChainPrefixes); the bundle passed in is
// left as it is.
func storedLayersBundleFull(t *testing.T, bundle ValueBundle) ValueBundle {
	t.Helper()
	bundle.Outputs = slices.Clone(bundle.Outputs)
	require.NoError(t, ResolveChainPrefixes(&bundle))
	return bundle
}

// A bundle the Cloud builds lists each stored layer once, and each output
// names its chain's layers by their place in the list, as a message on the
// wire does (ShareChainLayers): no output carries a copy of its chain's
// layers. Parts that sit on one base each carried a copy of every layer of
// the base, and the bundle's encoding then marshalled each copy to find the
// layers that were the same.
//
// Not parallel: it measures the process's allocation, which other tests
// running beside it would change.
func TestStoredBundleListsEachLayerOnce(t *testing.T) {
	const parts, baseLayers = 200, 40
	base := make([]snapshots.ExportLayer, baseLayers)
	for i := range base {
		base[i] = storedLayersTestLayer(fmt.Sprintf("listed-base-%d", i), fmt.Sprintf("layer %d of the base", i))
	}
	chains := make([][]snapshots.ExportLayer, parts)
	for i := range chains {
		chains[i] = append(slices.Clone(base), storedLayersTestLayer(fmt.Sprintf("listed-top-%d", i), "the part's own layer"))
	}
	ctx, cloud, roots := storedLayersBundleCloud(t, chains, nil)
	now := time.Now().Unix()

	// The bundle built and its layers listed once, as its encoding lists
	// them; the rest of its encoding is the same either way.
	share := func() {
		built, err := cloud.StoredBundle(ctx, roots, now, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ShareChainLayers(built.Bundle); err != nil {
			t.Fatal(err)
		}
	}
	share()
	const runs = 3
	var total uint64
	for range runs {
		runtime.GC()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		share()
		runtime.ReadMemStats(&after)
		total += after.TotalAlloc - before.TotalAlloc
	}
	perReference := float64(total) / runs / float64(parts*baseLayers)
	t.Logf("building a bundle of %d parts on a base of %d layers, its layers listed once, allocated %.0f bytes for each part's layer of the base", parts, baseLayers, perReference)

	got, err := cloud.StoredBundle(ctx, roots, now, nil)
	require.NoError(t, err)
	require.Len(t, got.Bundle.Outputs, parts)
	require.Len(t, got.Bundle.Layers, baseLayers+parts, "each layer listed once")
	for _, out := range got.Bundle.Outputs {
		require.NotNil(t, out.Chain)
		require.Empty(t, out.Chain.Layers, "no output carries its chain's layers")
		require.Len(t, out.Layers, baseLayers+1, "each output names its chain's layers by place")
	}
	require.Less(t, perReference, 300.0)
}

// A bundle the Cloud builds goes on the wire as its outputs' full chains
// would, each layer listed once by its encoding (ShareChainLayers): parts on
// a shared base, the same blob described otherwise, a layer in a pack, in
// another pack and in none, and a part with no layers. Two layers that
// encode alike are listed once even where the cache holds them apart: it
// tells layers apart by reflect.DeepEqual, so a layer whose annotations are
// an empty map is held apart from one with none, while the two encode
// alike, as the wire lists them.
func TestStoredBundleEncodesAsItsFullChains(t *testing.T) {
	t.Parallel()
	b1, b2 := storedLayersTestLayer("full-b1", "the base"), storedLayersTestLayer("full-b2", "the base's second layer")
	b1Again := storedLayersTestLayer("full-b1", "the same blob, from another snapshot's history")
	bare := storedLayersTestLayer("full-bare", "a layer with no annotations")
	bare.Descriptor.Annotations = nil
	bareEmpty := bare
	bareEmpty.Descriptor.Annotations = map[string]string{}
	packA, packB := testDigest("full-pack-a"), testDigest("full-pack-b")
	chains := [][]snapshots.ExportLayer{
		{b1, b2, storedLayersTestLayer("full-t1", "")},
		{b1, b2, storedLayersTestLayer("full-t2", "")},
		{b1Again, b2},
		{b1, b2},
		{bare, b2},
		{bareEmpty, b1},
		nil,
	}
	packs := []map[digest.Digest]digest.Digest{
		{b2.Descriptor.Digest: packA},
		{b2.Descriptor.Digest: packA},
		{b2.Descriptor.Digest: packB},
		nil,
		nil,
		nil,
		nil,
	}
	ctx, cloud, roots := storedLayersBundleCloud(t, chains, packs)

	// The cache holds the layer with no annotations and the one with empty
	// annotations apart.
	cloud.egraphMu.RLock()
	key, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	bareHeld := cloud.resultsByID[sharedResultID(roots[4])].storedParts[key].layers[0]
	bareEmptyHeld := cloud.resultsByID[sharedResultID(roots[5])].storedParts[key].layers[0]
	cloud.egraphMu.RUnlock()
	require.NotSame(t, bareHeld, bareEmptyHeld, "held apart")
	require.Equal(t, string(storedLayerEncoding(bare, "")), string(storedLayerEncoding(bareEmpty, "")), "but encoded alike")

	got, err := cloud.StoredBundle(ctx, roots, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Len(t, got.Bundle.Outputs, len(chains))
	require.Equal(t, storedLayersBundleWire(t, storedLayersBundleFull(t, got.Bundle)), storedLayersBundleWire(t, got.Bundle))
	var wire ValueBundle
	require.NoError(t, json.Unmarshal([]byte(storedLayersBundleWire(t, got.Bundle)), &wire))
	listed := 0
	for _, layer := range wire.Layers {
		if layer.Layer.Descriptor.Digest == bare.Descriptor.Digest {
			listed++
		}
	}
	require.Equal(t, 1, listed, "the layers that encode alike are listed once")
}
