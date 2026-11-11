package dagql

import (
	"reflect"
	"runtime"
	"sync"
	"weak"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
)

// A stored part's chain lists every layer from its base up, and the parts of
// a closure build on the same bases, so most of their layers are the same
// few. A blob-backed cache holds each distinct layer once, as an engine's
// snapshotter holds a parent snapshot once for every snapshot on it, and each
// stored part points to the layers of its chain. A merge on the wire lists
// each layer once for the same reason (ShareChainLayers).

// storedPart is a stored part as a blob-backed cache holds it: its offer
// without its chain's layers and packs, and the layers, which the cache
// holds once for every part that names them (storedLayers). Nothing changes
// a held layer: a caller that may change the offer takes a copy (offer).
type storedPart struct {
	held   PersistedPartOffer
	layers []*storedLayer
}

// storedLayer is a layer of a stored part's chain, and the pack the chain
// stores it in, if any (OfferedChain.Packs).
type storedLayer struct {
	layer snapshots.ExportLayer
	pack  digest.Digest
}

// offer returns the part's offer, its chain's layers and packs back in it:
// a copy the caller may change.
func (p storedPart) offer() PersistedPartOffer {
	// The held offer has no layers, whose creation time is all that a copy
	// can refuse.
	offer, _ := clonePartOffer(p.held)
	if len(p.layers) == 0 {
		return offer
	}
	offer.Chain.Layers = make([]snapshots.ExportLayer, len(p.layers))
	for i, layer := range p.layers {
		offer.Chain.Layers[i] = cloneExportLayer(layer.layer)
		if layer.pack != "" {
			if offer.Chain.Packs == nil {
				offer.Chain.Packs = map[digest.Digest]digest.Digest{}
			}
			offer.Chain.Packs[layer.layer.Descriptor.Digest] = layer.pack
		}
	}
	return offer
}

// address is the part's address, which the caller doesn't change.
func (p storedPart) address() PersistedPartAddress {
	return p.held.Address
}

// storedLayers holds each distinct layer of a blob-backed cache's stored
// parts once. It holds a layer weakly, by its blob: the parts that point to
// it keep it, and once none does the collector frees it and the table
// forgets it. The table is its own allocation, apart from the cache: the
// runtime keeps a layer's cleanup, and so the table, alive while the layer
// lives, and a cache the cleanup reached would keep its own layers alive.
type storedLayers struct {
	mu     sync.Mutex
	byBlob map[digest.Digest][]weak.Pointer[storedLayer]
}

// part returns offer as the cache holds it, its layers the table's. The
// offer is the caller's own copy, which the cache keeps: nothing else may
// change it after. Its packs name its chain's layers (OfferedChain.Packs):
// each layer keeps its own.
func (t *storedLayers) part(offer PersistedPartOffer) storedPart {
	if len(offer.Chain.Layers) == 0 {
		return storedPart{held: offer}
	}
	layers := make([]*storedLayer, len(offer.Chain.Layers))
	for i, layer := range offer.Chain.Layers {
		layers[i] = t.layer(storedLayer{layer: layer, pack: offer.Chain.Packs[layer.Descriptor.Digest]})
	}
	offer.Chain.Layers, offer.Chain.Packs = nil, nil
	return storedPart{held: offer, layers: layers}
}

// layer returns the table's layer equal to want, adding want if it has none.
// Layers of one blob can differ, in their history's description or
// creation time, or in the pack a chain names: each differing one is held
// apart, so that every part's offer is the one it was stored with.
func (t *storedLayers) layer(want storedLayer) *storedLayer {
	blob := want.layer.Descriptor.Digest
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, weakLayer := range t.byBlob[blob] {
		if held := weakLayer.Value(); held != nil && reflect.DeepEqual(*held, want) {
			return held
		}
	}
	held := &want
	if t.byBlob == nil {
		t.byBlob = map[digest.Digest][]weak.Pointer[storedLayer]{}
	}
	t.byBlob[blob] = append(t.byBlob[blob], weak.Make(held))
	runtime.AddCleanup(held, t.forget, blob)
	return held
}

// forget drops the blob's layers that the collector has freed.
func (t *storedLayers) forget(blob digest.Digest) {
	t.mu.Lock()
	defer t.mu.Unlock()
	live := t.byBlob[blob][:0]
	for _, weakLayer := range t.byBlob[blob] {
		if weakLayer.Value() != nil {
			live = append(live, weakLayer)
		}
	}
	if len(live) == 0 {
		delete(t.byBlob, blob)
		return
	}
	t.byBlob[blob] = live
}
