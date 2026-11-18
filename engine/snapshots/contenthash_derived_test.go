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

// Every derived snapshot is hashed from scratch and keeps a whole serialized
// tree of its own: the memory the trees take grows with edits × files.
func TestContentHashDerivedSnapshotsMemory(t *testing.T) {
	store := testutil.NewStore(t)
	before := bkcache.MetadataMemory(store.Manager)
	refs := buildEdits(t, store)
	after := bkcache.MetadataMemory(store.Manager)

	perTree := (after.ExternalBytes - before.ExternalBytes) / len(refs)
	t.Logf("%d snapshots (base + %d one-file edits) of %d files: %d bytes of trees in memory, %d per snapshot, %d per file",
		len(refs), derivedEdits, derivedBaseFiles, after.ExternalBytes-before.ExternalBytes, perTree, perTree/derivedBaseFiles)
	// The leak this documents: each one-file edit holds a full tree.
	require.Greater(t, after.ExternalBytes-before.ExternalBytes, len(refs)*derivedBaseFiles*50)
}
