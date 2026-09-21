package snapshots_test

import (
	"context"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	cerrdefs "github.com/containerd/errdefs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

const blobLabel = "containerd.io/gc.ref.content.blob"

// keepSnapshotOnly creates a lease that names the snapshot and nothing
// else: the shape of a durable owner whose content record is missing, and
// of the window between an import's pin release and its owner attachment.
func keepSnapshotOnly(t *testing.T, ctx context.Context, store *testutil.Store, id, snapshotID string) leases.Lease {
	t.Helper()
	keep, err := store.Leases.Create(ctx, leases.WithID(id))
	require.NoError(t, err)
	require.NoError(t, store.Leases.AddResource(ctx, keep, leases.Resource{ID: snapshotID, Type: "snapshots/native"}))
	return keep
}

func clearBlobLabel(t *testing.T, ctx context.Context, store *testutil.Store, snapshotID string) {
	t.Helper()
	info, err := store.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Equal(t, blobLabel, blobLabelKeyOf(info), "the snapshot carries its blob label")
	delete(info.Labels, blobLabel)
	_, err = store.Snapshots.Update(ctx, ctdsnapshots.Info{Name: snapshotID, Labels: info.Labels}, "labels")
	require.NoError(t, err)
}

func blobLabelKeyOf(info ctdsnapshots.Info) string {
	if _, ok := info.Labels[blobLabel]; ok {
		return blobLabel
	}
	return ""
}

// An imported layer's blob is bound to its snapshot: it survives the
// collector once the import's pin and every content lease are gone, as
// long as the snapshot itself is kept; a later export reuses it without a
// diff; and it goes when the snapshot goes. Clearing the label restores
// the old behavior, where the collector took the blob and the next export
// re-diffed the layer under a new digest.
func TestImportedLayerBlobIsBoundToItsSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	built, _ := producer.Build(t, nil, "a.txt", "bound to the snapshot")
	chain, err := built.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Uncompressed)})
	require.NoError(t, err)
	defer chain.Release(context.Background())
	require.Len(t, chain.Layers, 1)
	blob := chain.Layers[0].Descriptor.Digest

	imported, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: &testutil.Provider{InfoReaderProvider: chain.Provider}})
	require.NoError(t, err)
	snapshotID := imported.SnapshotID()
	info, err := consumer.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Equal(t, blob.String(), info.Labels[blobLabel], "the import labeled the snapshot with its blob")

	keep := keepSnapshotOnly(t, ctx, consumer, "keep-import", snapshotID)
	require.NoError(t, imported.Release(ctx), "the import's pin is gone")
	consumer.GC(t)
	_, err = consumer.Content.Info(ctx, blob)
	require.NoError(t, err, "the blob outlives its leases while the snapshot is kept")
	_, err = consumer.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)

	diffsBefore := consumer.Diffs.Load()
	kept, err := consumer.Manager.GetBySnapshotID(ctx, snapshotID, bkcache.NoUpdateLastUsed)
	require.NoError(t, err)
	exported, err := kept.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Uncompressed)})
	require.NoError(t, err)
	require.Len(t, exported.Layers, 1)
	require.Equal(t, blob, exported.Layers[0].Descriptor.Digest, "the export reuses the imported blob")
	require.Equal(t, diffsBefore, consumer.Diffs.Load(), "without diffing the snapshot")
	require.NoError(t, exported.Release(ctx))
	require.NoError(t, kept.Release(ctx))
	consumer.GC(t)

	// The label follows the snapshot: removing the snapshot releases the blob.
	require.NoError(t, consumer.Leases.Delete(ctx, keep))
	consumer.GC(t)
	_, err = consumer.Snapshots.Stat(ctx, snapshotID)
	require.True(t, cerrdefs.IsNotFound(err), "the snapshot is collected with its lease gone")
	_, err = consumer.Content.Info(ctx, blob)
	require.True(t, cerrdefs.IsNotFound(err), "and its blob with it")
}

// Control: a snapshot without the label, today's behavior, loses its blob
// to the collector while it is kept, and the next export diffs it again
// under a different digest.
func TestUnlabeledLayerBlobIsCollected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	built, _ := producer.Build(t, nil, "b.txt", "not bound")
	chain, err := built.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Gzip)})
	require.NoError(t, err)
	defer chain.Release(context.Background())
	blob := chain.Layers[0].Descriptor.Digest

	imported, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: &testutil.Provider{InfoReaderProvider: chain.Provider}})
	require.NoError(t, err)
	snapshotID := imported.SnapshotID()
	clearBlobLabel(t, ctx, consumer, snapshotID)
	keep := keepSnapshotOnly(t, ctx, consumer, "keep-unlabeled", snapshotID)
	require.NoError(t, imported.Release(ctx))
	consumer.GC(t)
	_, err = consumer.Content.Info(ctx, blob)
	require.True(t, cerrdefs.IsNotFound(err), "the unlabeled blob is collected although its snapshot is kept")

	diffsBefore := consumer.Diffs.Load()
	kept, err := consumer.Manager.GetBySnapshotID(ctx, snapshotID, bkcache.NoUpdateLastUsed)
	require.NoError(t, err)
	exported, err := kept.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Uncompressed)})
	require.NoError(t, err)
	require.Len(t, exported.Layers, 1)
	require.NotEqual(t, blob, exported.Layers[0].Descriptor.Digest, "the export had to diff the layer again, under another digest")
	require.Equal(t, diffsBefore+1, consumer.Diffs.Load())
	// The fresh diff is labeled in turn, so it stays with the snapshot.
	info, err := consumer.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Equal(t, exported.Layers[0].Descriptor.Digest.String(), info.Labels[blobLabel])
	require.NoError(t, exported.Release(ctx))
	require.NoError(t, kept.Release(ctx))
	require.NoError(t, consumer.Leases.Delete(ctx, keep))
}

// A diffed blob is bound the same way: a snapshot built here, exported
// once, keeps that blob across the collector with only the snapshot kept,
// and the next export reuses it.
func TestDiffedBlobIsBoundToItsSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	built, _ := store.Build(t, nil, "c.txt", "diffed here")
	snapshotID := built.SnapshotID()
	first, err := built.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Uncompressed)})
	require.NoError(t, err)
	blob := first.Layers[0].Descriptor.Digest
	require.NoError(t, first.Release(ctx))
	info, err := store.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Equal(t, blob.String(), info.Labels[blobLabel])

	keep := keepSnapshotOnly(t, ctx, store, "keep-diffed", snapshotID)
	require.NoError(t, built.Release(ctx))
	store.GC(t)
	_, err = store.Content.Info(ctx, blob)
	require.NoError(t, err, "the diffed blob outlives the build's lease")
	diffsBefore := store.Diffs.Load()
	kept, err := store.Manager.GetBySnapshotID(ctx, snapshotID, bkcache.NoUpdateLastUsed)
	require.NoError(t, err)
	again, err := kept.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Uncompressed)})
	require.NoError(t, err)
	require.Equal(t, blob, again.Layers[0].Descriptor.Digest)
	require.Equal(t, diffsBefore, store.Diffs.Load())
	require.NoError(t, again.Release(ctx))
	require.NoError(t, kept.Release(ctx))
	require.NoError(t, store.Leases.Delete(ctx, keep))
}
