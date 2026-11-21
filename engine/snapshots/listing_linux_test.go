package snapshots_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// listingsConfig exports listings, compressed as comp.
func listingsConfig(comp compression.Config) config.RefConfig {
	return config.RefConfig{Compression: comp, Listings: true}
}

// listingBody is n bytes of text that compresses, different for each seed.
func listingBody(seed string, n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "%s line %d of a file that is long enough to keep\n", seed, i*7919%104729)
	}
	return b.String()[:n]
}

// chainKinds names each of a chain's entries: a listing, a content blob or
// a tar.
func chainKinds(chain *bkcache.ExportChain) []string {
	kinds := make([]string, len(chain.Layers))
	for i, layer := range chain.Layers {
		switch {
		case bkcache.IsListing(layer.Descriptor):
			kinds[i] = "listing"
		case bkcache.IsContent(layer.Descriptor):
			kinds[i] = "content"
		default:
			kinds[i] = "tar"
		}
	}
	return kinds
}

// listingsTree builds two layers whose large files share one body three
// times over, across both layers, beside one large file of its own and
// small files.
func listingsTree(t *testing.T, store *testutil.Store, shared, own string) (a, ab bkcache.ImmutableRef) {
	t.Helper()
	a, _ = store.BuildWith(t, nil, func(root string) {
		writeExportFile(t, root, "a/shared-1.bin", shared)
		writeExportFile(t, root, "a/shared-2.bin", shared)
		writeExportFile(t, root, "a/small.txt", "small a")
	})
	ab, _ = store.BuildWith(t, a, func(root string) {
		writeExportFile(t, root, "b/own.bin", own)
		writeExportFile(t, root, "b/shared-3.bin", shared)
		writeExportFile(t, root, "b/small.txt", "small b")
	})
	return a, ab
}

// An export of listings carries each large file's bytes once in a chain,
// however many files and layers hold them: each content blob goes before the
// first listing that names it, so the chain's prefix for each snapshot holds
// everything its layers need, and the chain imports back whole and by
// prefix. An export of tars carries the same layers as tars.
func TestExportChainListingsCarryEachContentOnce(t *testing.T) {
	t.Parallel()
	shared, own := listingBody("shared", 100<<10), listingBody("own", 90<<10)
	for _, comp := range []compression.Type{compression.Uncompressed, compression.Zstd} {
		t.Run(comp.String(), func(t *testing.T) {
			t.Parallel()
			producer := testutil.NewStore(t)
			a, ab := listingsTree(t, producer, shared, own)
			chain := exportChains(t, []bkcache.ImmutableRef{ab}, listingsConfig(compression.New(comp)))[0]
			require.Equal(t, []string{"content", "listing", "content", "listing"}, chainKinds(chain))
			require.Equal(t, 2, chain.Prefixes[a.SnapshotID()])
			require.Equal(t, 4, chain.Prefixes[ab.SnapshotID()])
			require.Equal(t, 2, chain.Stats.Layers)
			require.Equal(t, 2, chain.Stats.Listings)
			require.Equal(t, 2, chain.Stats.Contents)
			var carried int64
			for _, layer := range chain.Layers {
				carried += layer.Descriptor.Size
			}
			if comp == compression.Uncompressed {
				require.Less(t, carried, int64(len(shared)+len(own)+64<<10), "the shared body is carried once")
			}

			imported := importChain(t, testutil.NewStore(t), supplied(chain, chain.Provider))
			for name, want := range map[string]string{
				"a/shared-1.bin": shared, "a/shared-2.bin": shared, "a/small.txt": "small a",
				"b/own.bin": own, "b/shared-3.bin": shared, "b/small.txt": "small b",
			} {
				testutil.CheckFile(t, imported, name, want)
			}

			prefix := importChain(t, testutil.NewStore(t), &bkcache.ExportChain{Layers: chain.Layers[:chain.Prefixes[a.SnapshotID()]], Provider: chain.Provider})
			testutil.CheckFile(t, prefix, "a/shared-1.bin", shared)
			testutil.CheckFile(t, prefix, "a/small.txt", "small a")

			tars := exportChains(t, []bkcache.ImmutableRef{ab}, config.RefConfig{Compression: compression.New(comp)})[0]
			require.Equal(t, []string{"tar", "tar"}, chainKinds(tars), "an export of tars builds the layers' tars")
		})
	}
}

// Correctness never rests on two engines compressing a file to the same
// bytes: one file's bytes, compressed two ways, are two content blobs and
// two listings, and both chains install, into one store or each into its
// own. All that is lost is that the store keeps the bytes twice.
func TestImportChainListingsOfOneFileCompressedTwoWays(t *testing.T) {
	t.Parallel()
	body := listingBody("one file", 200<<10)
	build := func(level *int) *bkcache.ExportChain {
		producer := testutil.NewStore(t)
		ref, _ := producer.BuildWith(t, nil, func(root string) {
			writeExportFile(t, root, "data/one.bin", body)
			writeExportFile(t, root, "data/small.txt", "small")
		})
		comp := compression.New(compression.Zstd)
		if level != nil {
			comp = comp.SetLevel(*level)
		}
		return exportChains(t, []bkcache.ImmutableRef{ref}, listingsConfig(comp))[0]
	}
	fastest := 1
	first, second := build(nil), build(&fastest)
	require.Equal(t, []string{"content", "listing"}, chainKinds(first))
	require.Equal(t, []string{"content", "listing"}, chainKinds(second))
	require.NotEqual(t, first.Layers[0].Descriptor.Digest, second.Layers[0].Descriptor.Digest, "the file is two content blobs")
	require.NotEqual(t, first.Layers[1].Descriptor.Digest, second.Layers[1].Descriptor.Digest, "and two listings")

	shared := testutil.NewStore(t)
	for _, chain := range []*bkcache.ExportChain{first, second} {
		ref := importChain(t, shared, supplied(chain, chain.Provider))
		testutil.CheckFile(t, ref, "data/one.bin", body)
		testutil.CheckFile(t, ref, "data/small.txt", "small")
		alone := importChain(t, testutil.NewStore(t), supplied(chain, chain.Provider))
		testutil.CheckFile(t, alone, "data/one.bin", body)
	}
}

// On overlayfs a chain of listings applies its layers at once, each rebuilt
// from its listing and contents, by containerd's applier and the engine's,
// whether the chain's blobs come from a provider or arrive by Await.
func TestImportChainOverlayListings(t *testing.T) {
	t.Parallel()
	shared, own := listingBody("shared", 100<<10), listingBody("own", 90<<10)
	producer := testutil.NewStore(t)
	_, ab := listingsTree(t, producer, shared, own)
	chain := exportChains(t, []bkcache.ImmutableRef{ab}, listingsConfig(compression.New(compression.Zstd)))[0]
	require.Equal(t, []string{"content", "listing", "content", "listing"}, chainKinds(chain))
	want := map[string]string{
		"a":              "dir 755",
		"a/shared-1.bin": "file 640 " + shared,
		"a/shared-2.bin": "file 640 " + shared,
		"a/small.txt":    "file 640 small a",
		"b":              "dir 755",
		"b/own.bin":      "file 640 " + own,
		"b/shared-3.bin": "file 640 " + shared,
		"b/small.txt":    "file 640 small b",
	}
	for _, engineApplier := range []bool{false, true} {
		for _, await := range []bool{false, true} {
			t.Run(fmt.Sprintf("engine applier %t, await %t", engineApplier, await), func(t *testing.T) {
				t.Parallel()
				consumer := testutil.NewOverlayStore(t)
				if engineApplier {
					consumer.UseEngineApplier(t)
				}
				imported := supplied(chain, chain.Provider)
				if await {
					imported = &bkcache.ExportChain{Layers: chain.Layers, Await: func(ctx context.Context, desc ocispecs.Descriptor) error {
						data, err := content.ReadBlob(ctx, chain.Provider, desc)
						if err != nil {
							return err
						}
						return content.WriteBlob(ctx, consumer.Content, "awaited-"+desc.Digest.Encoded(), bytes.NewReader(data), desc)
					}}
				}
				ref := importChain(t, consumer, imported)
				tree := testutil.OverlayTree(t, ref)
				for name, entry := range want {
					require.Equal(t, entry, tree[name], name)
				}
				requireParents(t, consumer, ref, 2)
			})
		}
	}
}
