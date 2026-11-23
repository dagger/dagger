package dagql

import (
	"encoding/json"
	"fmt"
	"hash/maphash"
	"reflect"
	"runtime"
	"slices"
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
	// wire is what the part's output adds to a merge's message, but its
	// layers' own entries (storedPartWire).
	wire int64
}

// storedLayer is a layer of a stored part's chain, and the pack the chain
// stores it in, if any (OfferedChain.Packs).
type storedLayer struct {
	layer snapshots.ExportLayer
	pack  digest.Digest
	// wire is the layer's entry, with its pack, in a merge's list of layers
	// (ShareChainLayers), with its separator.
	wire int64
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
// parts once. It holds a layer weakly, by its key: the parts that point to
// it keep it, and once none does the collector frees it and the table
// forgets it. The table is its own allocation, apart from the cache: the
// runtime keeps a layer's cleanup, and so the table, alive while the layer
// lives, and a cache the cleanup reached would keep its own layers alive.
//
// A layer's key is a hash of its encoding in a merge's list of layers,
// with its pack (storedLayerKey). Equal layers have one encoding, so a
// layer is compared only with the few that share its key. Engines describe
// one blob many ways, each build of the same content with its own creation
// time, and a store that several pull requests filled holds thousands of
// layers of one blob: compared with each of them, one by one under the
// table's lock, an upload's parts took seconds to store.
type storedLayers struct {
	mu    sync.Mutex
	byKey map[uint64][]weak.Pointer[storedLayer]
}

// storedLayerSeed seeds the keys of every table's layers (storedLayerKey).
// A key lives only in memory, and two tables never compare keys.
var storedLayerSeed = maphash.MakeSeed()

// part returns offer as the cache holds it, its layers the table's. The
// offer is the caller's own copy, which the cache keeps: nothing else may
// change it after. Its packs name its chain's layers (OfferedChain.Packs):
// each layer keeps its own.
func (t *storedLayers) part(offer PersistedPartOffer) storedPart {
	if len(offer.Chain.Layers) == 0 {
		return storedPart{held: offer, wire: storedPartWire(offer, nil)}
	}
	layers := make([]*storedLayer, len(offer.Chain.Layers))
	for i, layer := range offer.Chain.Layers {
		pack := offer.Chain.Packs[layer.Descriptor.Digest]
		encoded := storedLayerEncoding(layer, pack)
		layers[i] = t.layer(storedLayer{layer: layer, pack: pack, wire: storedLayerWire(encoded)}, storedLayerKey(layer, encoded))
	}
	offer.Chain.Layers, offer.Chain.Packs = nil, nil
	return storedPart{held: offer, layers: layers, wire: storedPartWire(offer, layers)}
}

// storedWireReference stands for each reference a part's output makes in a
// merge's message: its ordinal, its owner's and services' rows and its places
// in the bundle's layers. A bundle's references are smaller: no merge
// carries a million records or layers.
const storedWireReference = 999_999

// storedPartWire is the encoded size of the output that offer, held without
// its layers, becomes in a merge's message (ShareChainLayers), with its
// separator: its chain with a renewal key but neither its layers, their
// packs nor their addresses, which the bundle lists once for every output,
// and a reference for each layer. Its references count storedWireReference's
// digits, so the size is never less than a message carries for the output.
// A part that doesn't encode counts nothing: no merge can carry it either.
func storedPartWire(offer PersistedPartOffer, layers []*storedLayer) int64 {
	chain := offer.Chain
	chain.Layers, chain.Addresses, chain.Packs = nil, nil, nil
	key, _ := partAddressKey(offer.Address)
	chain.RenewalKey = fmt.Sprintf("%d/%s", uint64(1<<64-1), key)
	refs := make([]int, len(layers))
	for i := range refs {
		refs[i] = storedWireReference
	}
	owner := offer.Owner
	owner.DependencyIDs = make([]uint64, len(offer.Owner.DependencyIDs))
	for i := range owner.DependencyIDs {
		owner.DependencyIDs[i] = storedWireReference
	}
	value := offer.Value
	value.Services = slices.Clone(offer.Value.Services)
	for i := range value.Services {
		value.Services[i].ServiceResultID = storedWireReference
	}
	data, err := json.Marshal(TransferredOutput{Ordinal: storedWireReference, Address: offer.Address, State: "completed", Value: &value, Chain: &chain, Owner: &owner, Layers: refs})
	if err != nil {
		return 0
	}
	return int64(len(data)) + 1
}

// storedLayerWire is a layer's entry in a merge's list of layers, with the
// pack its chain stores it in and its separator, from its encoding
// (storedLayerEncoding), or 0 if it doesn't encode.
func storedLayerWire(encoded []byte) int64 {
	if encoded == nil {
		return 0
	}
	return int64(len(encoded)) + 1
}

// storedLayerEncoding is the layer's entry in a merge's list of layers,
// with the pack its chain stores it in, without its separator, or nil if it
// doesn't encode.
func storedLayerEncoding(layer snapshots.ExportLayer, pack digest.Digest) []byte {
	data, err := json.Marshal(BundleLayer{Layer: layer, Pack: pack})
	if err != nil {
		return nil
	}
	return data
}

// storedLayerKey is the key the table holds the layer under: a hash of its
// encoding with its pack, or of its blob if it doesn't encode. Layers that
// are equal encode alike, or both fail to, so they share a key; layers that
// differ almost never do, and the table tells those that do apart.
func storedLayerKey(layer snapshots.ExportLayer, encoded []byte) uint64 {
	if encoded == nil {
		return maphash.String(storedLayerSeed, string(layer.Descriptor.Digest))
	}
	return maphash.Bytes(storedLayerSeed, encoded)
}

// layer returns the table's layer equal to want, adding want if it has none,
// under want's key (storedLayerKey). Layers of one blob can differ, in their
// history's description or creation time, in whether the export computed
// the blob, or in the pack a chain names: each differing one is held apart,
// so that every part's offer is the one it was stored with.
func (t *storedLayers) layer(want storedLayer, key uint64) *storedLayer {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, weakLayer := range t.byKey[key] {
		if held := weakLayer.Value(); held != nil && reflect.DeepEqual(*held, want) {
			return held
		}
	}
	held := &want
	if t.byKey == nil {
		t.byKey = map[uint64][]weak.Pointer[storedLayer]{}
	}
	t.byKey[key] = append(t.byKey[key], weak.Make(held))
	runtime.AddCleanup(held, t.forget, key)
	return held
}

// forget drops the key's layers that the collector has freed.
func (t *storedLayers) forget(key uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	live := t.byKey[key][:0]
	for _, weakLayer := range t.byKey[key] {
		if weakLayer.Value() != nil {
			live = append(live, weakLayer)
		}
	}
	if len(live) == 0 {
		delete(t.byKey, key)
		return
	}
	t.byKey[key] = live
}
