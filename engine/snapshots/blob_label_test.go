package snapshots_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	cerrdefs "github.com/containerd/errdefs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

const blobLabel = "containerd.io/gc.ref.content.blob"

var uncompressed = config.RefConfig{Compression: compression.New(compression.Uncompressed)}

// buildSnapshot commits a one-file snapshot under a fresh owner lease, the
// way testutil.Build does but without cleanup, so the test owns every
// lifetime it asserts on. It returns the ref and the owner lease's ID.
func buildSnapshot(t *testing.T, store *testutil.Store, parent bkcache.ImmutableRef, name, value string) (bkcache.ImmutableRef, string) {
	t.Helper()
	ctx := context.Background()
	owner, err := store.Leases.Create(ctx, leases.WithRandomID())
	require.NoError(t, err)
	ctx = leases.WithLease(ctx, owner.ID)
	mut, err := store.Manager.New(ctx, parent)
	require.NoError(t, err)
	mounted, err := mut.Mount(ctx, false)
	require.NoError(t, err)
	mounter := bkcache.LocalMounter(mounted)
	root, err := mounter.Mount()
	require.NoError(t, err)
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(value), 0o640))
	require.NoError(t, os.Chtimes(path, testutil.FileTime, testutil.FileTime))
	require.NoError(t, mounter.Unmount())
	ref, err := mut.Commit(ctx)
	require.NoError(t, err)
	require.NoError(t, store.Manager.AttachLease(ctx, owner.ID, ref.SnapshotID()))
	return ref, owner.ID
}

// flatLeaseNamingOnly creates a lease in the engine's owner-lease shape,
// gc.flat, that names the snapshot and nothing else, without AttachLease:
// the shape under which a blob has no protection but a content resource.
func flatLeaseNamingOnly(t *testing.T, store *testutil.Store, id, snapshotID string) leases.Lease {
	t.Helper()
	ctx := context.Background()
	keep, err := store.Leases.Create(ctx, leases.WithID(id), func(l *leases.Lease) error {
		l.Labels = map[string]string{"containerd.io/gc.flat": time.Now().UTC().Format(time.RFC3339Nano)}
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, store.Leases.AddResource(ctx, keep, leases.Resource{ID: snapshotID, Type: "snapshots/native"}))
	return keep
}

func leaseResources(t *testing.T, store *testutil.Store, id string) (lease leases.Lease, snapshots, contents []string) {
	t.Helper()
	ctx := context.Background()
	all, err := store.Leases.List(ctx, "id=="+id)
	require.NoError(t, err)
	require.Len(t, all, 1, "lease %s exists", id)
	resources, err := store.Leases.ListResources(ctx, all[0])
	require.NoError(t, err)
	for _, r := range resources {
		switch r.Type {
		case "content":
			contents = append(contents, r.ID)
		default:
			snapshots = append(snapshots, r.ID)
		}
	}
	return all[0], snapshots, contents
}

func blobPresent(t *testing.T, store *testutil.Store, blob digest.Digest) bool {
	t.Helper()
	_, err := store.Content.Info(context.Background(), blob)
	if cerrdefs.IsNotFound(err) {
		return false
	}
	require.NoError(t, err)
	return true
}

func snapshotPresent(t *testing.T, store *testutil.Store, snapshotID string) bool {
	t.Helper()
	_, err := store.Snapshots.Stat(context.Background(), snapshotID)
	if cerrdefs.IsNotFound(err) {
		return false
	}
	require.NoError(t, err)
	return true
}

// exportOnce exports the kept snapshot's chain uncompressed and returns the
// top layer's digest and how many content writes the export made: a reuse
// writes nothing, a diff writes the blob. Writes, not the differ counter,
// because a store may diff through a differ the counter does not see.
func exportOnce(t *testing.T, store *testutil.Store, snapshotID string) (digest.Digest, int) {
	t.Helper()
	ctx := context.Background()
	writes := 0
	store.BeforeWrite = func([]byte) error { writes++; return nil }
	defer func() { store.BeforeWrite = nil }()
	kept, err := store.Manager.GetBySnapshotID(ctx, snapshotID, bkcache.NoUpdateLastUsed)
	require.NoError(t, err)
	chain, err := kept.ExportChain(ctx, uncompressed)
	require.NoError(t, err)
	require.NotEmpty(t, chain.Layers)
	dgst := chain.Layers[len(chain.Layers)-1].Descriptor.Digest
	require.NoError(t, chain.Release(ctx))
	require.NoError(t, kept.Release(ctx))
	return dgst, writes
}

// An imported layer's blob follows its snapshot into every lease that
// takes the snapshot, in the engine's own lease shape: after the import's
// pin is gone and the in-memory record dropped by a reload, a flat owner
// lease attached through AttachLease names the blob as a content resource
// read from the snapshot's label; the collector leaves the blob; the next
// export reuses it without a diff; and removing that owner collects the
// snapshot and the blob together.
func TestImportedLayerBlobIsBoundToItsSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	built, _ := producer.Build(t, nil, "a.txt", "bound to the snapshot")
	chain, err := built.ExportChain(ctx, uncompressed)
	require.NoError(t, err)
	defer chain.Release(context.Background())
	blob := chain.Layers[0].Descriptor.Digest

	imported, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: &testutil.Provider{InfoReaderProvider: chain.Provider}})
	require.NoError(t, err)
	snapshotID := imported.SnapshotID()
	info, err := consumer.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Equal(t, blob.String(), info.Labels[blobLabel], "the import labeled the snapshot with its blob")
	require.NoError(t, imported.Release(ctx), "the import's pin is gone")
	consumer.Reload(t)
	require.NoError(t, consumer.Manager.LoadPersistentMetadata(bkcache.PersistentMetadataRows{}))

	const owner = "dagql/result/owner-1"
	require.NoError(t, consumer.Manager.AttachLease(ctx, owner, snapshotID))
	lease, snapshots, contents := leaseResources(t, consumer, owner)
	require.NotEmpty(t, lease.Labels["containerd.io/gc.flat"], "the owner lease is flat")
	require.Equal(t, []string{snapshotID}, snapshots)
	require.Equal(t, []string{blob.String()}, contents, "the lease names the blob the label records, with no record in memory")
	consumer.GC(t)
	require.True(t, blobPresent(t, consumer, blob), "the blob outlives the import's pin under the owner lease")
	require.True(t, snapshotPresent(t, consumer, snapshotID))

	exported, writes := exportOnce(t, consumer, snapshotID)
	require.Equal(t, blob, exported, "the export reuses the imported blob")
	require.Zero(t, writes, "without writing anything: the recorded blob survived the reload")
	consumer.GC(t)
	require.True(t, blobPresent(t, consumer, blob), "the export's own pin left nothing behind that the owner did not hold")

	require.NoError(t, consumer.Manager.RemoveLease(ctx, owner))
	consumer.GC(t)
	require.False(t, snapshotPresent(t, consumer, snapshotID), "the snapshot is collected with its owner gone")
	require.False(t, blobPresent(t, consumer, blob), "and its blob with it")
}

// Control, the flat semantics: a flat lease that names only the snapshot,
// made without AttachLease, keeps the snapshot but not the blob, whatever
// the snapshot's label says; the next export diffs the layer again under
// another digest and labels that.
func TestFlatLeaseNamingOnlyTheSnapshotLosesTheBlob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	built, _ := producer.Build(t, nil, "b.txt", "not held by a content resource")
	chain, err := built.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Gzip)})
	require.NoError(t, err)
	defer chain.Release(context.Background())
	blob := chain.Layers[0].Descriptor.Digest

	imported, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: &testutil.Provider{InfoReaderProvider: chain.Provider}})
	require.NoError(t, err)
	snapshotID := imported.SnapshotID()
	info, err := consumer.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Equal(t, blob.String(), info.Labels[blobLabel])
	require.NoError(t, imported.Release(ctx))
	keep := flatLeaseNamingOnly(t, consumer, "flat-only", snapshotID)
	consumer.GC(t)
	require.True(t, snapshotPresent(t, consumer, snapshotID))
	require.False(t, blobPresent(t, consumer, blob), "a flat lease does not follow the snapshot's label to the blob")

	again, writes := exportOnce(t, consumer, snapshotID)
	require.NotEqual(t, blob, again, "the export had to diff the layer again, under another digest")
	require.Positive(t, writes, "and wrote the new blob")
	info, err = consumer.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Equal(t, again.String(), info.Labels[blobLabel], "the fresh diff is labeled in turn")
	require.NoError(t, consumer.Leases.Delete(ctx, keep))
}

// A diffed blob is bound the same way: once the build's own owner is gone
// and only a flat owner attached through AttachLease remains, the blob
// survives the collector, the next export reuses it, and removing that
// owner collects snapshot and blob.
func TestDiffedBlobIsBoundToItsSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	built, buildOwner := buildSnapshot(t, store, nil, "c.txt", "diffed here")
	snapshotID := built.SnapshotID()
	first, err := built.ExportChain(ctx, uncompressed)
	require.NoError(t, err)
	blob := first.Layers[0].Descriptor.Digest
	require.NoError(t, first.Release(ctx))
	require.NoError(t, built.Release(ctx))
	info, err := store.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Equal(t, blob.String(), info.Labels[blobLabel])

	const owner = "dagql/result/owner-diffed"
	require.NoError(t, store.Manager.AttachLease(ctx, owner, snapshotID))
	require.NoError(t, store.Manager.RemoveLease(ctx, buildOwner), "the build's own owner is gone")
	all, err := store.Leases.List(ctx)
	require.NoError(t, err)
	ids := []string{}
	for _, l := range all {
		ids = append(ids, l.ID)
	}
	require.Equal(t, []string{owner}, ids, "only the intended owner remains")
	_, _, contents := leaseResources(t, store, owner)
	require.Equal(t, []string{blob.String()}, contents)
	store.GC(t)
	require.True(t, blobPresent(t, store, blob), "the diffed blob outlives the build's owner")
	exported, writes := exportOnce(t, store, snapshotID)
	require.Equal(t, blob, exported)
	require.Zero(t, writes, "reused without a write")

	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
	store.GC(t)
	require.False(t, snapshotPresent(t, store, snapshotID))
	require.False(t, blobPresent(t, store, blob))
}

// A builtin image's blobs are held by one persistent lease with no
// snapshot at all: written under a temporary lease, pinned, the temporary
// lease deleted, they survive the collector and a second pin is a no-op;
// the startup sweeps leave the lease alone; deleting it releases them.
func TestBuiltinImageBlobsArePinned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	tmp, err := store.Leases.Create(ctx, leases.WithRandomID())
	require.NoError(t, err)
	var descs []ocispecs.Descriptor
	for _, data := range [][]byte{[]byte("builtin layer one"), []byte("builtin config")} {
		desc := ocispecs.Descriptor{MediaType: ocispecs.MediaTypeImageLayer, Digest: digest.FromBytes(data), Size: int64(len(data))}
		require.NoError(t, content.WriteBlob(leases.WithLease(ctx, tmp.ID), store.Content, "write-"+desc.Digest.Encoded(), bytes.NewReader(data), desc))
		descs = append(descs, desc)
	}
	const lease = "dagger/builtin-image/test"
	require.NoError(t, store.Manager.PinContent(ctx, lease, descs))
	require.NoError(t, store.Manager.PinContent(ctx, lease, descs), "pinning again is a no-op")
	require.NoError(t, store.Leases.Delete(ctx, tmp))
	store.GC(t)
	for _, desc := range descs {
		require.True(t, blobPresent(t, store, desc.Digest), "%s survives with only the builtin lease", desc.Digest)
	}
	pinned, _, contents := leaseResources(t, store, lease)
	require.Equal(t, "true", pinned.Labels[bkcache.BuiltinImageLeaseLabel])
	require.Len(t, contents, 2)

	require.NoError(t, store.Manager.DeleteStaleDaggerOwnerLeases(ctx, nil))
	require.NoError(t, bkcache.ReleaseTransferLeasesAfterRestart(ctx, store.Leases))
	store.GC(t)
	for _, desc := range descs {
		require.True(t, blobPresent(t, store, desc.Digest), "%s survives the startup sweeps", desc.Digest)
	}

	require.NoError(t, store.Manager.RemoveLease(ctx, lease))
	store.GC(t)
	for _, desc := range descs {
		require.False(t, blobPresent(t, store, desc.Digest), "%s is released with the lease", desc.Digest)
	}
}

// A label update that fails leaves no reusable blob record behind: the
// export fails before the metadata commit, the retry diffs again, labels
// the snapshot and commits, and the blob is then held by the owner lease.
func TestLabelUpdateFailureIsRepairedByTheRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := testutil.NewStore(t)
	built, buildOwner := buildSnapshot(t, store, nil, "d.txt", "label once refused")
	snapshotID := built.SnapshotID()
	refused := errors.New("label update refused")
	failures := 0
	store.BeforeSnapshotUpdate = func(_ context.Context, info ctdsnapshots.Info) error {
		if _, ok := info.Labels[blobLabel]; ok && failures == 0 {
			failures++
			return refused
		}
		return nil
	}
	_, err := built.ExportChain(ctx, uncompressed)
	require.ErrorIs(t, err, refused)
	info, err := store.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Empty(t, info.Labels[blobLabel], "no label after the refused update")
	writes := 0
	store.BeforeWrite = func([]byte) error { writes++; return nil }
	chain, err := built.ExportChain(ctx, uncompressed)
	store.BeforeWrite = nil
	require.NoError(t, err, "the retry succeeds")
	require.Positive(t, writes, "the retry diffed again: nothing reusable was committed by the failed export")
	blob := chain.Layers[0].Descriptor.Digest
	require.NoError(t, chain.Release(ctx))
	info, err = store.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Equal(t, blob.String(), info.Labels[blobLabel], "the retry labeled the snapshot")
	require.NoError(t, built.Release(ctx))

	const owner = "dagql/result/owner-repaired"
	require.NoError(t, store.Manager.AttachLease(ctx, owner, snapshotID))
	require.NoError(t, store.Manager.RemoveLease(ctx, buildOwner))
	store.GC(t)
	require.True(t, blobPresent(t, store, blob))
	exported, writes := exportOnce(t, store, snapshotID)
	require.Equal(t, blob, exported)
	require.Zero(t, writes)
	require.NoError(t, store.Manager.RemoveLease(ctx, owner))
}

// A forced export returns a compression variant of a snapshot's recorded
// blob; the label keeps naming the recorded blob. After a reload with empty
// metadata, a new flat owner holds the recorded blob, the earlier owners
// gone, and the next ordinary export reuses it.
func TestForcedVariantKeepsTheRecordedBlob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	built, _ := producer.Build(t, nil, "e.txt", "recorded uncompressed, forced to gzip")
	chain, err := built.ExportChain(ctx, uncompressed)
	require.NoError(t, err)
	defer chain.Release(context.Background())
	recorded := chain.Layers[0].Descriptor.Digest

	imported, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: &testutil.Provider{InfoReaderProvider: chain.Provider}})
	require.NoError(t, err)
	snapshotID := imported.SnapshotID()
	forced, err := imported.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Gzip).SetForce(true)})
	require.NoError(t, err)
	variant := forced.Layers[0].Descriptor.Digest
	require.NotEqual(t, recorded, variant, "the forced export returned a gzip variant")
	require.Equal(t, ocispecs.MediaTypeImageLayerGzip, forced.Layers[0].Descriptor.MediaType)
	require.NoError(t, forced.Release(ctx))
	info, err := consumer.Snapshots.Stat(ctx, snapshotID)
	require.NoError(t, err)
	require.Equal(t, recorded.String(), info.Labels[blobLabel], "the label still names the recorded blob")
	require.NoError(t, imported.Release(ctx), "the import's pin is gone")
	consumer.Reload(t)
	require.NoError(t, consumer.Manager.LoadPersistentMetadata(bkcache.PersistentMetadataRows{}))

	const owner = "dagql/result/owner-forced"
	require.NoError(t, consumer.Manager.AttachLease(ctx, owner, snapshotID))
	all, err := consumer.Leases.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1, "the new owner is the only lease")
	_, _, contents := leaseResources(t, consumer, owner)
	require.Equal(t, []string{recorded.String()}, contents, "the owner holds the recorded blob")
	consumer.GC(t)
	require.True(t, blobPresent(t, consumer, recorded), "the recorded blob survives with the earlier owners gone")

	exported, writes := exportOnce(t, consumer, snapshotID)
	require.Equal(t, recorded, exported, "the next ordinary export reuses the recorded blob")
	require.Zero(t, writes, "without writing anything")
	require.NoError(t, consumer.Manager.RemoveLease(ctx, owner))
}
