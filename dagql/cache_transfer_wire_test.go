package dagql

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// A bundle whose outputs' chains share their base layers lists each layer,
// and each blob's address, once when they're shared (ShareChainLayers), and
// resolves as it was: every chain with its own layers and its layers'
// addresses, and the packs it names, a chain that carried no address with
// none, and an address of a blob none of a chain's layers is kept on that
// chain. Chains given the same listed layer share it, as copies of an offer
// do: an offered layer is never written after it's built.
func TestValueBundleWireListsLayersOnce(t *testing.T) {
	layer := func(name string) snapshots.ExportLayer {
		return snapshots.ExportLayer{
			Descriptor: ocispecs.Descriptor{
				MediaType:   ocispecs.MediaTypeImageLayerGzip,
				Digest:      digest.FromString(name),
				Size:        int64(len(name)),
				Annotations: map[string]string{"containerd.io/uncompressed": digest.FromString("raw " + name).String()},
			},
			Description: "exec go build " + name,
		}
	}
	address := func(l snapshots.ExportLayer) BlobAddress {
		return BlobAddress{URL: "https://blobs.example/" + l.Descriptor.Digest.Encoded() + "?X-Amz-Signature=" + fmt.Sprint(len(l.Description)), ExpiresAtUnix: 1793430000}
	}
	var base []snapshots.ExportLayer
	for i := range 20 {
		base = append(base, layer(fmt.Sprint("base-", i)))
	}
	bundle := ValueBundle{Version: ValueBundleVersion, Roots: []TransferredRoot{{Ordinal: 1}}}
	for i := range 50 {
		bundle.Values = append(bundle.Values, TransferredValue{Ordinal: TransferOrdinal(i + 1), SenderNumber: uint64(100 + i)})
		layers := append(append([]snapshots.ExportLayer{}, base...), layer(fmt.Sprint("own-", i)))
		chain := &OfferedChain{Layers: layers, RenewalKey: fmt.Sprintf("%d/fs", 100+i), Addresses: map[digest.Digest]BlobAddress{}}
		for _, l := range layers {
			chain.Addresses[l.Descriptor.Digest] = address(l)
		}
		if i%2 == 0 {
			// Its own small layer is stored in a pack with other blobs: the
			// pack is part of what the chain is.
			own := layers[len(layers)-1].Descriptor.Digest
			chain.Packs = map[digest.Digest]digest.Digest{own: digest.FromString(fmt.Sprint("pack-", i/10))}
		}
		bundle.Outputs = append(bundle.Outputs, TransferredOutput{
			Ordinal: TransferOrdinal(i + 1),
			Address: PersistedPartAddress{Part: "fs"},
			State:   "completed",
			Value:   &SnapshotValue{Kind: "directory"},
			Chain:   chain,
			Owner:   &PersistedOfferOwner{},
		})
	}
	// An export's chain carries no address.
	bundle.Outputs = append(bundle.Outputs, TransferredOutput{Ordinal: 1, Address: PersistedPartAddress{Part: "rootfs"}, State: "completed", Chain: &OfferedChain{Layers: []snapshots.ExportLayer{layer("unaddressed")}}})
	// A chain's address of a blob that isn't one of its layers.
	other := layer("other")
	bundle.Outputs[0].Chain.Addresses[other.Descriptor.Digest] = address(other)

	shared, err := ShareChainLayers(bundle)
	require.NoError(t, err)
	data, err := json.Marshal(shared)
	require.NoError(t, err)
	repeated, err := json.Marshal(bundle)
	require.NoError(t, err)
	require.Less(t, len(data)*5, len(repeated), "each layer and address once: %d bytes against %d", len(data), len(repeated))

	var decoded ValueBundle
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NoError(t, ResolveChainPrefixes(&decoded))
	require.Empty(t, decoded.Layers)
	require.Empty(t, decoded.Addresses)
	require.Equal(t, bundle.Outputs, decoded.Outputs)
	require.Equal(t, bundle.Roots, decoded.Roots)
	require.Len(t, decoded.Values, len(bundle.Values))

	first, second := decoded.Outputs[0].Chain, decoded.Outputs[1].Chain
	require.Equal(t, reflect.ValueOf(first.Layers[0].Descriptor.Annotations).Pointer(), reflect.ValueOf(second.Layers[0].Descriptor.Annotations).Pointer(), "a base layer is listed once and shared")
	require.NotEqual(t, reflect.ValueOf(first.Addresses).Pointer(), reflect.ValueOf(second.Addresses).Pointer(), "each chain has its own addresses")
}

// A merge's message names each packed layer's pack once, with the layer, as
// the Cloud holds a stored layer: outputs whose chains store shared layers in
// a pack carry no packs of their own, and resolve to their chains with their
// packs. A layer one chain stores alone and another in a second pack is
// listed once for each, every output naming the one its chain stores.
func TestValueBundleWireListsPacksOnce(t *testing.T) {
	layer := func(name string) snapshots.ExportLayer {
		return snapshots.ExportLayer{
			Descriptor: ocispecs.Descriptor{
				MediaType: ocispecs.MediaTypeImageLayerGzip,
				Digest:    digest.FromString(name),
				Size:      int64(len(name)),
			},
			Description: "exec go build " + name,
		}
	}
	pack := digest.FromString("pack")
	var base []snapshots.ExportLayer
	for i := range 40 {
		base = append(base, layer(fmt.Sprint("packed-", i)))
	}
	bundle := func(packed bool) ValueBundle {
		b := ValueBundle{Version: ValueBundleVersion, Roots: []TransferredRoot{{Ordinal: 1}}}
		for i := range 50 {
			chain := &OfferedChain{Layers: append([]snapshots.ExportLayer{}, base...), RenewalKey: fmt.Sprintf("%d/fs", 100+i)}
			if packed {
				chain.Packs = map[digest.Digest]digest.Digest{}
				for _, l := range base {
					chain.Packs[l.Descriptor.Digest] = pack
				}
			}
			b.Values = append(b.Values, TransferredValue{Ordinal: TransferOrdinal(i + 1), SenderNumber: uint64(100 + i)})
			b.Outputs = append(b.Outputs, TransferredOutput{Ordinal: TransferOrdinal(i + 1), Address: PersistedPartAddress{Part: "fs"}, State: "completed", Chain: chain})
		}
		return b
	}
	size := func(b ValueBundle) int {
		shared, err := ShareChainLayers(b)
		require.NoError(t, err)
		data, err := json.Marshal(shared)
		require.NoError(t, err)
		return len(data)
	}
	packed := bundle(true)
	entry := len(`,"pack":""`) + len(pack.String())
	require.LessOrEqual(t, size(packed)-size(bundle(false)), len(base)*entry, "each layer's pack once, not once an output")

	other := digest.FromString("another pack")
	packed.Outputs = append(packed.Outputs,
		TransferredOutput{Ordinal: 1, Address: PersistedPartAddress{Part: "alone"}, State: "completed", Chain: &OfferedChain{Layers: base[:1]}},
		TransferredOutput{Ordinal: 2, Address: PersistedPartAddress{Part: "other"}, State: "completed", Chain: &OfferedChain{Layers: base[:1], Packs: map[digest.Digest]digest.Digest{base[0].Descriptor.Digest: other}}})
	shared, err := ShareChainLayers(packed)
	require.NoError(t, err)
	for _, out := range shared.Outputs {
		require.Empty(t, out.Chain.Packs, "output %d names its layers' packs by place", out.Ordinal)
	}
	data, err := json.Marshal(shared)
	require.NoError(t, err)
	var decoded ValueBundle
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.NoError(t, ResolveChainPrefixes(&decoded))
	require.Equal(t, packed.Outputs, decoded.Outputs)
}

// A bundle of version 5, from before a merge listed its layers once, is
// refused, not misread, as an older cache refuses this version: an engine
// and a service of different versions fail loudly rather than install
// chains with no layers.
func TestValueBundleOfTheVersionBeforeSharedLayersRefused(t *testing.T) {
	c, err := NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.CloseDiscardingPersistence() })
	_, err = c.MergeValues(t.Context(), CloudCacheID, ValueBundle{Version: 5, Roots: []TransferredRoot{{Ordinal: 1}}})
	require.ErrorContains(t, err, "unsupported value bundle version 5")
}
