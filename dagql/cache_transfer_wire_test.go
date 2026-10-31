package dagql

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// A bundle whose outputs' chains share their base layers lists each layer,
// and each blob's address, once when they're shared (ShareChainLayers), and
// resolves as it was: every
// chain with its own layers and its layers' addresses, a chain that carried
// no address with none, and an address of a blob none of a chain's layers
// is kept on that chain.
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
	bundle := ValueBundle{Version: valueBundleVersion, Roots: []TransferredRoot{{Ordinal: 1}}}
	for i := range 50 {
		bundle.Values = append(bundle.Values, TransferredValue{Ordinal: TransferOrdinal(i + 1), SenderNumber: uint64(100 + i)})
		layers := append(append([]snapshots.ExportLayer{}, base...), layer(fmt.Sprint("own-", i)))
		chain := &OfferedChain{Layers: layers, RenewalKey: fmt.Sprintf("%d/fs", 100+i), Addresses: map[digest.Digest]BlobAddress{}}
		for _, l := range layers {
			chain.Addresses[l.Descriptor.Digest] = address(l)
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
