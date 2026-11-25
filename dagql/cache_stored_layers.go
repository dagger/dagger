package dagql

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/maphash"
	"runtime"
	"slices"
	"sync"
	"weak"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
)

// A part's chain lists every layer from its base up, and the parts of a
// closure build on the same bases, so most of their layers are the same few.
// A cache holds each distinct layer once, as an engine's snapshotter holds a
// parent snapshot once for every snapshot on it, and each part it holds
// points to the layers of its chain: a blob-backed cache's stored parts
// (storedPart), and the parts the Cloud offers an engine (partOffer). A merge
// on the wire lists each layer once for the same reason (ShareChainLayers).

// layeredOffer is a part's offer as a cache holds it: the offer without its
// chain's layers and packs, and the layers, which the cache holds once for
// every part that names them (storedLayers). Nothing changes a held layer: a
// caller that may change the offer takes a copy (offer, record).
type layeredOffer struct {
	held   PersistedPartOffer
	layers []*storedLayer
}

// storedPart is a stored part as a blob-backed cache holds it.
type storedPart struct {
	layeredOffer
	// wire is what the part's output adds to a merge's message, but its
	// layers' own entries (storedPartWire).
	wire int64
}

// storedLayer is a layer of a held part's chain, and the pack the chain
// stores it in, if any (OfferedChain.Packs).
type storedLayer struct {
	layer snapshots.ExportLayer
	pack  digest.Digest
	// wire is the layer's entry, with its pack, in a merge's list of layers
	// (ShareChainLayers), with its separator.
	wire int64
	// key is the key the table holds it under (storedLayerKey): layers that
	// are equal, or that encode alike, share it.
	key uint64
}

// offer returns the part's offer, its chain's layers and packs back in it:
// a copy the caller may change, layers and all.
func (p layeredOffer) offer() PersistedPartOffer {
	// The held offer has no layers, whose creation time is all that a copy
	// can refuse.
	offer, _ := clonePartOffer(p.held)
	if len(p.layers) == 0 {
		return offer
	}
	offer.Chain.Layers = make([]snapshots.ExportLayer, len(p.layers))
	for i, layer := range p.layers {
		offer.Chain.Layers[i] = cloneExportLayer(layer.layer)
	}
	offer.Chain.Packs = p.packs()
	return offer
}

// record returns the part's offer, its chain's layers and packs back in it,
// as the cache holds it: it shares the held offer's maps and slices, and what
// its layers refer to, so the caller changes nothing of it, and copies it
// (clonePartOffer) to change it.
func (p layeredOffer) record() PersistedPartOffer {
	offer := p.held
	if len(p.layers) > 0 {
		offer.Chain.Layers, offer.Chain.Packs = p.chainLayers(), p.packs()
	}
	return offer
}

// chainLayers returns the chain's layers, in a slice of the caller's own:
// what they refer to is the cache's, which nothing writes.
func (p layeredOffer) chainLayers() []snapshots.ExportLayer {
	layers := make([]snapshots.ExportLayer, len(p.layers))
	for i, layer := range p.layers {
		layers[i] = layer.layer
	}
	return layers
}

// packs maps each of the chain's layers stored in a pack to its pack, as
// OfferedChain.Packs does, or is nil when none is.
func (p layeredOffer) packs() map[digest.Digest]digest.Digest {
	var packs map[digest.Digest]digest.Digest
	for _, layer := range p.layers {
		if layer.pack != "" {
			if packs == nil {
				packs = map[digest.Digest]digest.Digest{}
			}
			packs[layer.layer.Descriptor.Digest] = layer.pack
		}
	}
	return packs
}

// heldOffer returns the part's offer without its chain's layers and packs,
// which a bundle lists apart (storedBundleBuild.listLayer): a copy the caller
// may change.
func (p layeredOffer) heldOffer() PersistedPartOffer {
	// The held offer has no layers, whose creation time is all that a copy
	// can refuse.
	offer, _ := clonePartOffer(p.held)
	return offer
}

// address is the part's address, which the caller doesn't change.
func (p layeredOffer) address() PersistedPartAddress {
	return p.held.Address
}

// sameMeaning reports whether two offers that one table holds mean the same,
// as sameOfferMeaning does: equal in everything but their chain's blob
// addresses. The table holds equal layers, each with its pack, as one.
func (p layeredOffer) sameMeaning(q layeredOffer) bool {
	return slices.Equal(p.layers, q.layers) && sameOfferMeaning(p.held, q.held)
}

// storedLayers holds each distinct layer of the parts a cache holds once. It
// holds a layer weakly, by its key: the parts that point to it keep it, and
// once none does the collector frees it and the table forgets it. The table
// is its own allocation, apart from the cache: the runtime keeps a layer's
// cleanup, and so the table, alive while the layer lives, and a cache the
// cleanup reached would keep its own layers alive.
//
// A layer's key is a hash of its blob, its history's description and
// creation time, whether the export computed it, and its pack
// (storedLayerKey): a layer is compared only with the few that share its
// key. Engines describe one blob many ways, each build of the same content
// with its own creation time, and a store that several pull requests filled
// holds thousands of layers of one blob: compared with each of them, one by
// one under the table's lock, an upload's parts took seconds to store. An
// engine holds the layers of every part the Cloud offers it, millions of a
// set's references to them, so the key is made without encoding the layer.
type storedLayers struct {
	mu    sync.Mutex
	byKey map[uint64][]weak.Pointer[storedLayer]
}

// storedLayerSeed seeds the keys of every table's layers (storedLayerKey).
// A key lives only in memory, and two tables never compare keys.
var storedLayerSeed = maphash.MakeSeed()

// part returns offer as a blob-backed cache stores it, its layers the
// table's (hold).
func (t *storedLayers) part(offer PersistedPartOffer) storedPart {
	held := t.hold(offer)
	return storedPart{layeredOffer: held, wire: storedPartWire(held.held, held.layers)}
}

// hold returns offer as the cache holds it, its layers the table's. The
// offer is the caller's own copy, which the cache keeps: nothing else may
// change it after. Its packs name its chain's layers (OfferedChain.Packs):
// each layer keeps its own.
func (t *storedLayers) hold(offer PersistedPartOffer) layeredOffer {
	if len(offer.Chain.Layers) == 0 {
		return layeredOffer{held: offer}
	}
	layers := make([]*storedLayer, len(offer.Chain.Layers))
	t.mu.Lock()
	for i, layer := range offer.Chain.Layers {
		layers[i] = t.layerLocked(layer, offer.Chain.Packs[layer.Descriptor.Digest])
	}
	t.mu.Unlock()
	offer.Chain.Layers, offer.Chain.Packs = nil, nil
	return layeredOffer{held: offer, layers: layers}
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
// blob, its history's description and creation time, whether the export
// computed it, and its pack. Layers that are equal share it, and so do layers
// that encode alike, which a bundle lists once (storedBundleBuild.listLayer):
// each of those is the same in all five. Layers that differ in them almost
// never share it, and the table tells apart those that do.
func storedLayerKey(layer snapshots.ExportLayer, pack digest.Digest) uint64 {
	var h maphash.Hash
	h.SetSeed(storedLayerSeed)
	h.WriteString(string(layer.Descriptor.Digest))
	h.WriteByte(0)
	h.WriteString(layer.Description)
	h.WriteByte(0)
	h.WriteString(string(pack))
	if layer.Computed {
		h.WriteByte(1)
	}
	if layer.CreatedAt != nil {
		var created [8]byte
		binary.LittleEndian.PutUint64(created[:], uint64(layer.CreatedAt.UnixNano()))
		h.Write(created[:])
	}
	return h.Sum64()
}

// layerLocked returns the table's layer equal to layer in pack, adding it if
// the table has none, under its key (storedLayerKey). Layers of one blob can
// differ, in their history's description or creation time, in whether the
// export computed the blob, or in the pack a chain names: each differing one
// is held apart, so that every part's offer is the one it was stored with.
// Layers are told apart as reflect.DeepEqual tells them (sameExportLayer).
// Requires t.mu.
func (t *storedLayers) layerLocked(layer snapshots.ExportLayer, pack digest.Digest) *storedLayer {
	key := storedLayerKey(layer, pack)
	for _, weakLayer := range t.byKey[key] {
		if held := weakLayer.Value(); held != nil && held.pack == pack && sameExportLayer(held.layer, layer) {
			return held
		}
	}
	held := &storedLayer{layer: layer, pack: pack, wire: storedLayerWire(storedLayerEncoding(layer, pack)), key: key}
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
