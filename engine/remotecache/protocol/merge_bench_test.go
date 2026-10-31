package protocol

import (
	"fmt"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots"
)

// sharedLayersMerge is a merge shaped as test-base's large placement merges
// are: 2,000 outputs, each a chain of 30 base layers its closure shares and
// one of its own, with a download address for every layer.
func sharedLayersMerge() Merge {
	layer := func(name string) snapshots.ExportLayer {
		return snapshots.ExportLayer{
			Descriptor: ocispecs.Descriptor{
				MediaType:   ocispecs.MediaTypeImageLayerGzip,
				Digest:      digest.FromString(name),
				Size:        int64(len(name)),
				Annotations: map[string]string{"containerd.io/uncompressed": digest.FromString("raw " + name).String()},
			},
			Description: "exec /usr/local/go/bin/go build -o /out " + name,
		}
	}
	address := func(l snapshots.ExportLayer) dagql.BlobAddress {
		return dagql.BlobAddress{URL: "https://blobs.example.com/org/" + l.Descriptor.Digest.Encoded() + "?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIAEXAMPLE%2F20261031%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20261031T000000Z&X-Amz-Expires=3600&X-Amz-SignedHeaders=host&X-Amz-Signature=" + l.Descriptor.Digest.Encoded(), ExpiresAtUnix: 1793430000}
	}
	var base []snapshots.ExportLayer
	for i := range 30 {
		base = append(base, layer(fmt.Sprint("base-", i)))
	}
	bundle := dagql.ValueBundle{Version: 6, Roots: []dagql.TransferredRoot{{Ordinal: 1}}}
	for i := range 2000 {
		bundle.Values = append(bundle.Values, dagql.TransferredValue{Ordinal: dagql.TransferOrdinal(i + 1), SenderNumber: uint64(i + 1)})
		layers := append(append([]snapshots.ExportLayer{}, base...), layer(fmt.Sprint("own-", i)))
		chain := &dagql.OfferedChain{Layers: layers, RenewalKey: fmt.Sprintf("%d/fs", i+1), Addresses: map[digest.Digest]dagql.BlobAddress{}}
		for _, l := range layers {
			chain.Addresses[l.Descriptor.Digest] = address(l)
		}
		bundle.Outputs = append(bundle.Outputs, dagql.TransferredOutput{
			Ordinal: dagql.TransferOrdinal(i + 1),
			Address: dagql.PersistedPartAddress{Part: "fs"},
			State:   "completed",
			Value:   &dagql.SnapshotValue{Kind: "directory"},
			Chain:   chain,
			Owner:   &dagql.PersistedOfferOwner{},
		})
	}
	return Merge{Bundle: bundle}
}

func BenchmarkMergeSharedLayers(b *testing.B) {
	merge := sharedLayersMerge()
	env, err := NewEnvelope(1, 0, TypeMerge, merge)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(env.Body)), "bytes/msg")
		for b.Loop() {
			if _, err := NewEnvelope(1, 0, TypeMerge, merge); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("decode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var got Merge
			if err := env.Decode(&got); err != nil {
				b.Fatal(err)
			}
			// As the engine's merge resolves it.
			if err := dagql.ResolveChainPrefixes(&got.Bundle); err != nil {
				b.Fatal(err)
			}
		}
	})
}
