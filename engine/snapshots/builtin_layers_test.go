package snapshots_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// builtinStoreWith plays the engine's builtin image store: a local content
// store holding the given layers of chain, copied from its provider.
func builtinStoreWith(t *testing.T, chain *bkcache.ExportChain, layers ...int) content.Store {
	t.Helper()
	ctx := context.Background()
	store, err := local.NewStore(t.TempDir())
	require.NoError(t, err)
	for _, i := range layers {
		desc := chain.Layers[i].Descriptor
		reader, err := chain.Provider.ReaderAt(ctx, desc)
		require.NoError(t, err)
		data, err := io.ReadAll(io.NewSectionReader(reader, 0, reader.Size()))
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.NoError(t, content.WriteBlob(ctx, store, "builtin-"+desc.Digest.Encoded(), bytes.NewReader(data), desc))
	}
	return store
}

// twoLayerChain builds two layers in producer and exports them gzip, as
// the builtin store holds its images.
func twoLayerChain(t *testing.T, producer *testutil.Store) *bkcache.ExportChain {
	t.Helper()
	base, _ := producer.Build(t, nil, "base.txt", "base layer")
	top, _ := producer.Build(t, base, "top.txt", "top layer")
	chain, err := top.ExportChain(context.Background(), config.RefConfig{Compression: compression.New(compression.Gzip)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = chain.Release(context.Background()) })
	require.Len(t, chain.Layers, 2)
	return chain
}

// A chain whose layers are all in the builtin store imports with no read
// from the chain's provider: the blobs come from the engine's own files,
// are bound to the imported snapshots like any other layer, and are held
// for the engine's lifetime under the builtin layers lease.
func TestChainImportTakesBuiltinLayersFromTheBuiltinStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	chain := twoLayerChain(t, producer)
	consumer.WithBuiltin(t, builtinStoreWith(t, chain, 0, 1))
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}

	imported, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: provider})
	require.NoError(t, err)
	require.Zero(t, provider.Reads.Load(), "nothing was read from the chain's provider")
	testutil.CheckFile(t, imported, "base.txt", "base layer")
	testutil.CheckFile(t, imported, "top.txt", "top layer")
	snapshotID := imported.SnapshotID()
	info, err := consumer.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Equal(t, chain.Layers[1].Descriptor.Digest.String(), info.Labels[blobLabel], "the copied blob is the snapshot's blob")
	require.NoError(t, imported.Release(ctx))

	_, _, contents := leaseResources(t, consumer, bkcache.BuiltinLayersLeaseID)
	require.ElementsMatch(t, []string{chain.Layers[0].Descriptor.Digest.String(), chain.Layers[1].Descriptor.Digest.String()}, contents, "both blobs are held by the builtin layers lease")
	consumer.GC(t)
	for _, layer := range chain.Layers {
		require.True(t, blobPresent(t, consumer, layer.Descriptor.Digest), "%s outlives the import with no snapshot owner", layer.Descriptor.Digest)
	}
	require.False(t, snapshotPresent(t, consumer, snapshotID), "the unowned snapshot itself is collected")
}

// A chain with one layer outside the builtin store reads exactly that
// layer from the provider.
func TestChainImportReadsOnlyNonBuiltinLayersFromTheProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	chain := twoLayerChain(t, producer)
	consumer.WithBuiltin(t, builtinStoreWith(t, chain, 0))
	var read []digest.Digest
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider, BeforeRead: func(_ context.Context, desc ocispecs.Descriptor) error {
		read = append(read, desc.Digest)
		return nil
	}}

	imported, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: provider})
	require.NoError(t, err)
	require.EqualValues(t, 1, provider.Reads.Load())
	require.Equal(t, []digest.Digest{chain.Layers[1].Descriptor.Digest}, read, "only the layer the builtin store lacks")
	testutil.CheckFile(t, imported, "top.txt", "top layer")
	require.NoError(t, imported.Release(ctx))
	_, _, contents := leaseResources(t, consumer, bkcache.BuiltinLayersLeaseID)
	require.Equal(t, []string{chain.Layers[0].Descriptor.Digest.String()}, contents, "only the builtin layer is held for the engine's lifetime")
}

// failingBuiltin has every layer but cannot be read from.
type failingBuiltin struct {
	content.InfoReaderProvider
}

func (f failingBuiltin) ReaderAt(context.Context, ocispecs.Descriptor) (content.ReaderAt, error) {
	return nil, errors.New("builtin store unreadable")
}

// A builtin store lookup that fails falls through to the provider, as
// today, and the import still succeeds.
func TestChainImportFallsThroughWhenTheBuiltinStoreFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	chain := twoLayerChain(t, producer)
	consumer.WithBuiltin(t, failingBuiltin{InfoReaderProvider: builtinStoreWith(t, chain, 0, 1)})
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}

	imported, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: provider})
	require.NoError(t, err)
	require.EqualValues(t, 2, provider.Reads.Load(), "both layers came from the provider")
	testutil.CheckFile(t, imported, "top.txt", "top layer")
	require.NoError(t, imported.Release(ctx))
	all, err := consumer.Leases.List(ctx, "id=="+bkcache.BuiltinLayersLeaseID)
	require.NoError(t, err)
	require.Empty(t, all, "nothing was taken from the builtin store")
}

// Only the exact size matches: a layer descriptor that says another size
// for a digest the builtin store has is not taken from the store. With a
// zero size, which the content copy treats as unknown, the provider path
// succeeds on the valid bytes; with a larger size the provider path fails
// on the short read, as it would for any such chain. Either way the layer
// is asked of the provider and never enters the builtin layers lease.
func TestChainImportRefusesBuiltinLayersOfAnotherSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		size    func(int64) int64
		imports bool
	}{
		{name: "zero", size: func(int64) int64 { return 0 }, imports: true},
		{name: "larger", size: func(n int64) int64 { return n + 1 }, imports: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
			chain := twoLayerChain(t, producer)
			consumer.WithBuiltin(t, builtinStoreWith(t, chain, 0, 1))
			layers := []bkcache.ExportLayer{chain.Layers[0], chain.Layers[1]}
			layers[0].Descriptor.Size = tc.size(layers[0].Descriptor.Size)
			var read []digest.Digest
			provider := &testutil.Provider{InfoReaderProvider: chain.Provider, BeforeRead: func(_ context.Context, desc ocispecs.Descriptor) error {
				read = append(read, desc.Digest)
				return nil
			}}
			imported, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: layers, Provider: provider})
			require.Equal(t, []digest.Digest{chain.Layers[0].Descriptor.Digest}, read[:1], "the mismatched layer was asked of the provider first")
			if !tc.imports {
				require.Error(t, err, "a larger size is a short read on the provider path")
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				require.Equal(t, 1, len(read), "the import stopped at that layer")
				all, err := consumer.Leases.List(ctx, "id=="+bkcache.BuiltinLayersLeaseID)
				require.NoError(t, err)
				require.Empty(t, all, "no builtin layers lease: the store's blob was refused and nothing else was taken")
				return
			}
			require.NoError(t, err, "a zero size is unknown to the copy; the provider's bytes are valid")
			require.Equal(t, []digest.Digest{chain.Layers[0].Descriptor.Digest}, read, "only the mismatched layer came from the provider")
			testutil.CheckFile(t, imported, "top.txt", "top layer")
			require.NoError(t, imported.Release(ctx))
			_, _, contents := leaseResources(t, consumer, bkcache.BuiltinLayersLeaseID)
			require.Equal(t, []string{chain.Layers[1].Descriptor.Digest.String()}, contents, "only the layer with the exact size is held from the builtin store")
		})
	}
}
