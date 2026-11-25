package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// An engine holds each layer of the chains the Cloud offers it once, as the
// Cloud holds its stored parts' (TestStoredPartsHoldSharedLayersOnce): a
// restore of parts that sit on one base grows the engine's live heap by far
// less than a copy of the base, with its packs, for each part. Each restored
// entry's offer still reads back as the Cloud stored it. A layer's copy
// takes about 500 bytes, and a part's pointer to a held layer 8; what each
// entry costs apart from its chain is measured on a restore of the same
// parts with no base, and left out.
//
// Not parallel: it measures the process's live heap, which other tests
// running beside it would change.
func TestRestoredOffersHoldSharedLayersOnce(t *testing.T) {
	const parts, baseLayers = 200, 40
	base := make([]snapshots.ExportLayer, baseLayers)
	for i := range base {
		base[i] = storedLayersTestLayer(fmt.Sprintf("restored-base-%d", i), fmt.Sprintf("layer %d of the base", i))
	}
	pack := testDigest("restored-base-pack")

	// restore restores parts whose chains are the first depth layers of the
	// base, in a pack, and a layer of each part's own, and returns how much
	// the engine's live heap grew.
	restore := func(depth int) int64 {
		chains := make([][]snapshots.ExportLayer, parts)
		packs := make([]map[digest.Digest]digest.Digest, parts)
		for i := range chains {
			chains[i] = append(slices.Clone(base[:depth]), storedLayersTestLayer(fmt.Sprintf("restored-top-%d-%d", depth, i), "the part's own layer"))
			packs[i] = map[digest.Digest]digest.Digest{}
			for _, layer := range base[:depth] {
				packs[i][layer.Descriptor.Digest] = pack
			}
		}
		ctx, cloud, roots := storedLayersBundleCloud(t, chains, packs)
		built, err := cloud.StoredBundle(ctx, roots, time.Now().Unix(), nil)
		require.NoError(t, err)
		// The bundle as an engine reads it off the wire, each listed layer
		// decoded once.
		var bundle ValueBundle
		require.NoError(t, json.Unmarshal([]byte(storedLayersBundleWire(t, built.Bundle)), &bundle))

		ectx := cacheTestContext(context.Background())
		engine, err := NewCache(ectx, filepath.Join(t.TempDir(), "engine.db"), nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, engine.CloseDiscardingPersistence()) })
		srv := newDagqlServerForTest(t, &persistCodecRoot{})
		srv.InstallObject(NewClass(srv, ClassOpts[*storedBenchValue]{}))
		ectx = srvToContext(ContextWithCache(ectx, engine), srv)

		before := storedLayersTestLiveHeap()
		reply, err := engine.RestoreValues(ectx, bundle, nil, "")
		require.NoError(t, err)
		grown := storedLayersTestLiveHeap() - before
		runtime.KeepAlive(bundle)
		t.Cleanup(func() { require.NoError(t, reply.Release(context.Background())) })
		require.True(t, reply.Committed)

		// Each root's entry holds the offer the Cloud stored, told by its own
		// top layer.
		byTop := map[digest.Digest]int{}
		for i, chain := range chains {
			byTop[chain[len(chain)-1].Descriptor.Digest] = i
		}
		engine.egraphMu.RLock()
		defer engine.egraphMu.RUnlock()
		seen := map[int]bool{}
		for _, root := range reply.Roots {
			res := engine.resultsByID[sharedResultID(root.Number)]
			require.NotNil(t, res, "root %d", root.Ordinal)
			offers, err := res.attachedOffersLocked()
			require.NoError(t, err)
			require.Len(t, offers, 1, "root %d is offered its part", root.Ordinal)
			got := offers[0].Chain
			require.NotEmpty(t, got.Layers)
			i, ok := byTop[got.Layers[len(got.Layers)-1].Descriptor.Digest]
			require.True(t, ok, "root %d's chain ends in a part's own layer", root.Ordinal)
			seen[i] = true
			require.True(t, reflect.DeepEqual(chains[i], got.Layers), "part %d's chain", i)
			if depth == 0 {
				require.Empty(t, got.Packs, "part %d's packs", i)
			} else {
				require.Equal(t, packs[i], got.Packs, "part %d's packs", i)
			}
		}
		require.Len(t, seen, parts)
		return grown
	}

	alone := restore(0)
	based := restore(baseLayers)
	perReference := float64(based-alone) / float64(parts*baseLayers)
	t.Logf("restoring %d parts on a base of %d layers grew the engine's live heap by %d bytes, and by %d with no base: %.0f bytes for each part's reference to a base layer", parts, baseLayers, based, alone, perReference)
	require.Less(t, perReference, 64.0, "each part's reference to a base layer took %.0f bytes", perReference)
}
