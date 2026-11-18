package snapshots_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/contenthash"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

const (
	hashedSnapshots = 30
	hashedDirs      = 4
	hashedFiles     = 25 // per directory
)

// buildHashed builds a snapshot of small distinct files, hashes it as a
// directory digest would, and waits for its content-hash tree to be saved
// into the snapshot's metadata.
func buildHashed(t *testing.T, store *testutil.Store, i int) (bkcache.ImmutableRef, string) {
	t.Helper()
	ctx := context.Background()
	ref, owner := store.BuildWith(t, nil, func(root string) {
		for d := range hashedDirs {
			dir := filepath.Join(root, fmt.Sprintf("dir%d", d))
			require.NoError(t, os.MkdirAll(dir, 0o755))
			for f := range hashedFiles {
				p := filepath.Join(dir, fmt.Sprintf("file%d.txt", f))
				require.NoError(t, os.WriteFile(p, fmt.Appendf(nil, "snapshot %d dir %d file %d", i, d, f), 0o644))
			}
		}
	})
	_, err := contenthash.Checksum(ctx, ref, "/", contenthash.ChecksumOpts{})
	require.NoError(t, err)
	// The tree is saved in the background after a checksum updates it.
	require.Eventually(t, func() bool {
		_, err := ref.(bkcache.RefMetadata).GetExternal(bkcache.ContentHashMetadataKey)
		return err == nil
	}, 10*time.Second, time.Millisecond)
	return ref, owner
}

// collect does what a GC pass does to the store: release the idle shared
// mounts, which hold their snapshots, collect, and forget what was collected.
func collect(t *testing.T, store *testutil.Store) {
	t.Helper()
	require.NoError(t, mountReleaser(t, store).ReleaseIdleSharedMounts())
	store.GC(t)
	forgetter, ok := store.Manager.(bkcache.CollectedSnapshotForgetter)
	require.True(t, ok)
	require.NoError(t, forgetter.ForgetCollectedSnapshots(context.Background()))
}

// Each hashed snapshot keeps its serialized content-hash tree with the
// manager's metadata, on disk. Releasing the refs and the owners, and
// collecting the snapshots, must not leave those trees, or the metadata,
// behind.
func TestContentHashMetadataMemory(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	before := bkcache.MetadataMemory(store.Manager)

	// A snapshot that stays owned keeps its tree, and its digests.
	kept, _ := buildHashed(t, store, -1)
	keptDigest, err := contenthash.Checksum(ctx, kept, "/dir1", contenthash.ChecksumOpts{})
	require.NoError(t, err)
	keptTree, err := kept.(bkcache.RefMetadata).GetExternal(bkcache.ContentHashMetadataKey)
	require.NoError(t, err)
	withKept := bkcache.MetadataMemory(store.Manager)

	refs := make([]bkcache.ImmutableRef, 0, hashedSnapshots)
	owners := make([]string, 0, hashedSnapshots)
	for i := range hashedSnapshots {
		ref, owner := buildHashed(t, store, i)
		refs = append(refs, ref)
		owners = append(owners, owner)
	}
	hashed := bkcache.MetadataMemory(store.Manager)
	t.Logf("before: %+v", before)
	t.Logf("hashed %d more snapshots of %d files: %+v (%d tree bytes per snapshot)",
		hashedSnapshots, hashedDirs*hashedFiles, hashed,
		(hashed.ContentHashBytes()-withKept.ContentHashBytes())/hashedSnapshots)
	require.Equal(t, withKept.Metadata+hashedSnapshots, hashed.Metadata)
	require.Greater(t, hashed.ContentHashBytes(), withKept.ContentHashBytes())
	require.Zero(t, hashed.ExternalBytes, "the trees are kept on disk")

	for i, ref := range refs {
		require.NoError(t, ref.Release(ctx))
		require.NoError(t, store.Manager.RemoveLease(ctx, owners[i]))
	}
	released := bkcache.MetadataMemory(store.Manager)
	t.Logf("released refs and owners: %+v", released)
	// The snapshots exist until they are collected, and so do their trees.
	require.Equal(t, hashed, released)

	collect(t, store)
	for _, ref := range refs {
		_, err := store.Snapshots.Stat(ctx, ref.SnapshotID())
		require.Error(t, err, "snapshot %s should be collected", ref.SnapshotID())
	}
	collected := bkcache.MetadataMemory(store.Manager)
	t.Logf("collected snapshots: %+v", collected)
	require.Equal(t, withKept, collected)

	tree, err := kept.(bkcache.RefMetadata).GetExternal(bkcache.ContentHashMetadataKey)
	require.NoError(t, err)
	require.Equal(t, keptTree, tree)
	dgst, err := contenthash.Checksum(ctx, kept, "/dir1", contenthash.ChecksumOpts{})
	require.NoError(t, err)
	require.Equal(t, keptDigest, dgst)
	// The kept snapshot can still be opened by its ID.
	reopened, err := store.Manager.GetBySnapshotID(ctx, kept.SnapshotID())
	require.NoError(t, err)
	require.NoError(t, reopened.Release(ctx))
}

// Forgetting runs while snapshots are made and committed, and drops none of
// theirs: an entry it sees before its walk has its snapshot already.
func TestForgetCollectedSnapshotsKeepsNewSnapshots(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	forgetter := store.Manager.(bkcache.CollectedSnapshotForgetter)

	done := make(chan struct{})
	errs := make(chan error, 1)
	go func() {
		defer close(errs)
		for {
			select {
			case <-done:
				return
			default:
			}
			if err := forgetter.ForgetCollectedSnapshots(ctx); err != nil {
				errs <- err
				return
			}
		}
	}()
	refs := make([]bkcache.ImmutableRef, 0, 20)
	for i := range 20 {
		ref, _ := store.Build(t, nil, fmt.Sprintf("f%d", i), "data")
		refs = append(refs, ref)
	}
	close(done)
	require.NoError(t, <-errs)

	for i, ref := range refs {
		_, ok, err := store.Manager.SnapshotRecordMetadata(ctx, ref.SnapshotID())
		require.NoError(t, err)
		require.True(t, ok, "metadata of live snapshot %s dropped", ref.SnapshotID())
		testutil.CheckFile(t, ref, fmt.Sprintf("f%d", i), "data")
	}
}

// Hash records loaded at startup for snapshots nobody opened are dropped once
// their snapshots are collected, too, and so are the blobs and imported
// layers recorded for them.
func TestContentHashPendingRecordsForgottenWhenCollected(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	kept, _ := buildHashed(t, store, 0)
	dropped, droppedOwner := buildHashed(t, store, 1)
	keptID, droppedID := kept.SnapshotID(), dropped.SnapshotID()
	require.NoError(t, kept.Release(ctx))
	require.NoError(t, dropped.Release(ctx))

	rows := store.Manager.PersistentMetadataRows()
	require.Len(t, rows.ContentHashes, 2)
	snapshotRows := func(id string) bkcache.PersistentMetadataRows {
		blob := digest.FromString("blob of " + id)
		diff := digest.FromString("diff of " + id)
		return bkcache.PersistentMetadataRows{
			SnapshotContent: []bkcache.SnapshotContentRow{{SnapshotID: id, Digest: blob}},
			ImportedByBlob:  []bkcache.ImportedLayerBlobRow{{BlobDigest: blob, SnapshotID: id}},
			ImportedByDiff:  []bkcache.ImportedLayerDiffRow{{DiffID: diff, SnapshotID: id}},
		}
	}
	keptRows, droppedRows := snapshotRows(keptID), snapshotRows(droppedID)
	rows.SnapshotContent = append(keptRows.SnapshotContent, droppedRows.SnapshotContent...)
	rows.ImportedByBlob = append(keptRows.ImportedByBlob, droppedRows.ImportedByBlob...)
	rows.ImportedByDiff = append(keptRows.ImportedByDiff, droppedRows.ImportedByDiff...)

	store.Reload(t)
	require.NoError(t, store.Manager.LoadPersistentMetadata(rows))
	loaded := bkcache.MetadataMemory(store.Manager)
	require.Equal(t, 2, loaded.ContentHashFiles)
	require.Zero(t, loaded.ExternalBytes, "loaded trees are kept on disk")

	require.NoError(t, store.Manager.RemoveLease(ctx, droppedOwner))
	collect(t, store)
	_, err := store.Snapshots.Stat(ctx, droppedID)
	require.Error(t, err)
	got := bkcache.MetadataMemory(store.Manager)
	require.Equal(t, 1, got.ContentHashFiles)
	require.Less(t, got.ContentHashBytes(), loaded.ContentHashBytes())
	left := store.Manager.PersistentMetadataRows()
	require.Equal(t, keptRows.SnapshotContent, left.SnapshotContent)
	require.Equal(t, keptRows.ImportedByBlob, left.ImportedByBlob)
	require.Equal(t, keptRows.ImportedByDiff, left.ImportedByDiff)

	// The kept snapshot's loaded tree is still there when it is opened.
	opCtx, release := operationLease(t, store)
	defer release()
	reopened, err := store.Manager.LeaseExistingSnapshot(opCtx, keptID)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Release(ctx)) }()
	_, err = reopened.(bkcache.RefMetadata).GetExternal(bkcache.ContentHashMetadataKey)
	require.NoError(t, err)
}
