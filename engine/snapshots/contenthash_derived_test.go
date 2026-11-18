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
	derivedBaseFiles = 500
	derivedEdits     = 10
)

// hashRoot hashes ref's root, as a directory digest does, and waits for the
// content-hash tree it makes to be saved into ref's metadata.
func hashRoot(t *testing.T, ref bkcache.ImmutableRef) digest.Digest {
	t.Helper()
	dgst, err := contenthash.Checksum(context.Background(), ref, "/", contenthash.ChecksumOpts{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, err := ref.(bkcache.RefMetadata).GetExternal(bkcache.ContentHashMetadataKey)
		return err == nil
	}, 10*time.Second, time.Millisecond)
	return dgst
}

// buildEdits builds a base snapshot of derivedBaseFiles files and
// derivedEdits snapshots on it, each changing one file, as an agent editing a
// checkout does with withNewFile, and hashes each.
func buildEdits(t *testing.T, store *testutil.Store) []bkcache.ImmutableRef {
	t.Helper()
	base, _ := store.BuildWith(t, nil, func(root string) {
		for i := range derivedBaseFiles {
			dir := filepath.Join(root, fmt.Sprintf("pkg%02d", i/50))
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%03d.go", i)), fmt.Appendf(nil, "package p // %d", i), 0o644))
		}
	})
	hashRoot(t, base)
	refs := []bkcache.ImmutableRef{base}
	for k := range derivedEdits {
		edited, _ := store.BuildWith(t, base, func(root string) {
			p := filepath.Join(root, fmt.Sprintf("pkg%02d", k%10), fmt.Sprintf("file%03d.go", k*50+k%10))
			require.NoError(t, os.WriteFile(p, fmt.Appendf(nil, "package p // edit %d", k), 0o644))
		})
		hashRoot(t, edited)
		refs = append(refs, edited)
	}
	return refs
}

// Every derived snapshot is hashed from scratch and has a whole serialized
// tree of its own, so the trees grow with edits × files. They are kept on
// disk, not in memory.
func TestContentHashDerivedSnapshotsMemory(t *testing.T) {
	store := testutil.NewStore(t)
	before := bkcache.MetadataMemory(store.Manager)
	refs := buildEdits(t, store)
	after := bkcache.MetadataMemory(store.Manager)

	trees := after.ContentHashBytes() - before.ContentHashBytes()
	perTree := trees / len(refs)
	t.Logf("%d snapshots (base + %d one-file edits) of %d files: %d bytes of trees (%d in memory, %d on disk), %d per snapshot, %d per file",
		len(refs), derivedEdits, derivedBaseFiles, trees, after.ExternalBytes, after.ContentHashFileBytes, perTree, perTree/derivedBaseFiles)
	require.Greater(t, trees, len(refs)*derivedBaseFiles*50)
	require.Equal(t, len(refs), after.ContentHashFiles)
	require.Zero(t, after.ExternalBytes, "no tree is held in memory")
}

// A tree kept on disk gives the digests a tree kept in memory gives, before
// and after a checkpoint and restart: the cache context that parsed it is
// dropped, and the next checksum reads the tree back.
func TestContentHashFilesRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	refs := buildEdits(t, store)
	want := make([]map[string]digest.Digest, len(refs))
	paths := []string{"/", "/pkg03", "/pkg03/file153.go", "/pkg07"}
	for i, ref := range refs {
		want[i] = map[string]digest.Digest{}
		for _, p := range paths {
			dgst, err := contenthash.Checksum(ctx, ref, p, contenthash.ChecksumOpts{})
			require.NoError(t, err)
			want[i][p] = dgst
		}
	}
	// Edits change digests: the base and each edit differ at the root.
	require.NotEqual(t, want[0]["/"], want[1]["/"])

	ids := make([]string, len(refs))
	trees := make([][]byte, len(refs))
	for i, ref := range refs {
		ids[i] = ref.SnapshotID()
		tree, err := ref.(bkcache.RefMetadata).GetExternal(bkcache.ContentHashMetadataKey)
		require.NoError(t, err)
		trees[i] = tree
		require.NoError(t, ref.Release(ctx))
	}

	// Checkpoint and restart: the checkpoint has every tree, byte for byte.
	rows := store.Manager.PersistentMetadataRows()
	got := map[string][]byte{}
	for _, row := range rows.ContentHashes {
		got[row.SnapshotID] = row.Data
	}
	for i, id := range ids {
		require.Equal(t, trees[i], got[id], "checkpointed tree of %s", id)
	}
	store.Reload(t)
	reloaded := bkcache.MetadataMemory(store.Manager)
	require.Equal(t, len(refs), reloaded.ContentHashFiles)
	require.Zero(t, reloaded.ExternalBytes)

	opCtx, release := operationLease(t, store)
	defer release()
	for i, id := range ids {
		// Drop the parsed tree, so the checksums read it back from disk.
		ref, err := store.Manager.LeaseExistingSnapshot(opCtx, id)
		require.NoError(t, err)
		md := ref.(bkcache.RefMetadata)
		contenthash.ClearCacheContext(md)
		tree, err := md.GetExternal(bkcache.ContentHashMetadataKey)
		require.NoError(t, err)
		require.Equal(t, trees[i], tree)
		for _, p := range paths {
			dgst, err := contenthash.Checksum(ctx, ref, p, contenthash.ChecksumOpts{})
			require.NoError(t, err)
			require.Equal(t, want[i][p], dgst, "digest of %s in snapshot %d after restart", p, i)
		}
		require.NoError(t, ref.Release(ctx))
	}
}
