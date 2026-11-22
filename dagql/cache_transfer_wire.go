package dagql

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
)

// ShareChainLayers returns the bundle with its outputs' chains' layers
// listed once, each with the pack its chains store it in, in its Layers, and
// each blob's download address once, in its Addresses: each output names its
// chain's layers by their place in Layers (TransferredOutput.Layers), and its
// chain keeps all but its layers and their packs, such as its renewal key,
// and of its addresses only those the bundle's don't give. A closure's
// outputs build on the same bases, and each chain lists every layer from its
// base up, so a merge of a large closure otherwise lists each layer, with its
// pack and its address, about a hundred times over. ResolveChainPrefixes
// gives each output its chain back. The bundle passed in is left as it is.
func ShareChainLayers(bundle ValueBundle) (ValueBundle, error) {
	if !slices.ContainsFunc(bundle.Outputs, func(out TransferredOutput) bool { return out.Chain != nil }) {
		return bundle, nil
	}
	places := map[string]int{}
	bundle.Outputs = slices.Clone(bundle.Outputs)
	for i := range bundle.Outputs {
		out := &bundle.Outputs[i]
		if out.Chain == nil {
			continue
		}
		// The chain keeps all but its layers, their packs and their
		// addresses, such as its renewal key.
		chain := *out.Chain
		chain.Layers, chain.Addresses = nil, nil
		packs := maps.Clone(out.Chain.Packs)
		refs := make([]int, 0, len(out.Chain.Layers))
		own := map[digest.Digest]bool{}
		for _, layer := range out.Chain.Layers {
			blob := layer.Descriptor.Digest
			own[blob] = true
			listed := BundleLayer{Layer: layer, Pack: packs[blob]}
			delete(packs, blob)
			data, err := json.Marshal(listed)
			if err != nil {
				return ValueBundle{}, err
			}
			place, ok := places[string(data)]
			if !ok {
				place = len(bundle.Layers)
				places[string(data)] = place
				bundle.Layers = append(bundle.Layers, listed)
			}
			refs = append(refs, place)
		}
		chain.Packs = nil
		if len(packs) > 0 {
			chain.Packs = packs
		}
		for blob, address := range out.Chain.Addresses {
			shared, ok := bundle.Addresses[blob]
			switch {
			case own[blob] && !ok:
				if bundle.Addresses == nil {
					bundle.Addresses = map[digest.Digest]BlobAddress{}
				}
				bundle.Addresses[blob] = address
			case own[blob] && shared == address:
			default:
				if chain.Addresses == nil {
					chain.Addresses = map[digest.Digest]BlobAddress{}
				}
				chain.Addresses[blob] = address
			}
		}
		out.Chain, out.Layers = &chain, refs
	}
	return bundle, nil
}

// resolveSharedLayers gives each output that names its chain's layers in
// the bundle's Layers (ShareChainLayers) its chain's layers, which chains
// naming the same listed layer share, as copies of an offer do
// (clonePartOffer: an offered layer is never written), their packs, and the
// bundle's addresses of their blobs where the chain has
// none of its own, in place; then the bundle's Layers and Addresses are
// cleared. A chain that carried no address for one of its layers gets the
// bundle's address for that blob, if another chain carried one: an address
// of its own blob.
func resolveSharedLayers(bundle *ValueBundle) error {
	for i := range bundle.Outputs {
		out := &bundle.Outputs[i]
		if out.Layers == nil {
			continue
		}
		if out.Chain == nil {
			return fmt.Errorf("output %d names layers without a chain", out.Ordinal)
		}
		chain := *out.Chain
		chain.Layers = make([]snapshots.ExportLayer, 0, len(out.Layers))
		addresses := maps.Clone(chain.Addresses)
		packs := maps.Clone(chain.Packs)
		for _, place := range out.Layers {
			if place < 0 || place >= len(bundle.Layers) {
				return fmt.Errorf("output %d names layer %d of %d", out.Ordinal, place, len(bundle.Layers))
			}
			listed := bundle.Layers[place]
			layer := listed.Layer
			chain.Layers = append(chain.Layers, layer)
			blob := layer.Descriptor.Digest
			if listed.Pack != "" {
				if packs == nil {
					packs = map[digest.Digest]digest.Digest{}
				}
				packs[blob] = listed.Pack
			}
			if address, ok := bundle.Addresses[blob]; ok {
				if _, own := addresses[blob]; !own {
					if addresses == nil {
						addresses = map[digest.Digest]BlobAddress{}
					}
					addresses[blob] = address
				}
			}
		}
		chain.Addresses, chain.Packs = addresses, packs
		out.Chain, out.Layers = &chain, nil
	}
	bundle.Layers, bundle.Addresses = nil, nil
	return nil
}

// cloneExportLayer copies a layer and what its descriptor refers to.
func cloneExportLayer(layer snapshots.ExportLayer) snapshots.ExportLayer {
	layer.Descriptor.Annotations = maps.Clone(layer.Descriptor.Annotations)
	layer.Descriptor.URLs = slices.Clone(layer.Descriptor.URLs)
	layer.Descriptor.Data = slices.Clone(layer.Descriptor.Data)
	if layer.Descriptor.Platform != nil {
		platform := *layer.Descriptor.Platform
		platform.OSFeatures = slices.Clone(platform.OSFeatures)
		layer.Descriptor.Platform = &platform
	}
	if layer.CreatedAt != nil {
		created := *layer.CreatedAt
		layer.CreatedAt = &created
	}
	return layer
}
