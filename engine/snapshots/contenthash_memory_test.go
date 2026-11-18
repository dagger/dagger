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

// Each hashed snapshot keeps its serialized content-hash tree in the
// manager's in-memory metadata. Releasing the refs and the owners, and
// collecting the snapshots, must not leave those trees behind.
func TestContentHashMetadataMemory(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	before := bkcache.MetadataMemory(store.Manager)

	refs := make([]bkcache.ImmutableRef, 0, hashedSnapshots)
	owners := make([]string, 0, hashedSnapshots)
	for i := range hashedSnapshots {
		ref, owner := buildHashed(t, store, i)
		refs = append(refs, ref)
		owners = append(owners, owner)
	}
	hashed := bkcache.MetadataMemory(store.Manager)
	t.Logf("before: %+v", before)
	t.Logf("hashed %d snapshots of %d files: %+v (%d tree bytes per snapshot)",
		hashedSnapshots, hashedDirs*hashedFiles, hashed,
		(hashed.ExternalBytes-before.ExternalBytes)/hashedSnapshots)
	require.GreaterOrEqual(t, hashed.Metadata-before.Metadata, hashedSnapshots)
	require.Greater(t, hashed.ExternalBytes, before.ExternalBytes)

	for i, ref := range refs {
		require.NoError(t, ref.Release(ctx))
		require.NoError(t, store.Manager.RemoveLease(ctx, owners[i]))
	}
	released := bkcache.MetadataMemory(store.Manager)
	t.Logf("released refs and owners: %+v", released)

	// As a GC pass does: idle shared mounts hold their snapshots.
	require.NoError(t, mountReleaser(t, store).ReleaseIdleSharedMounts())
	store.GC(t)
	for _, ref := range refs {
		_, err := store.Snapshots.Stat(ctx, ref.SnapshotID())
		require.Error(t, err, "snapshot %s should be collected", ref.SnapshotID())
	}
	collected := bkcache.MetadataMemory(store.Manager)
	t.Logf("collected snapshots: %+v", collected)

	// The leak: nothing drops the metadata of a collected snapshot, so every
	// hashed snapshot's tree stays in memory for the life of the engine.
	require.Equal(t, hashed.Metadata, collected.Metadata)
	require.Equal(t, hashed.ExternalBytes, collected.ExternalBytes)
}
