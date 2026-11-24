package snapshots_test

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/contenthash"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/fsutil"
	fstypes "github.com/dagger/dagger/internal/fsutil/types"
	"github.com/dagger/dagger/util/hashutil"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"github.com/zeebo/xxh3"
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

// hostFileInfo is a file as a host import receives it: its stat, and the
// XXH3 digest the client computed.
type hostFileInfo struct {
	stat *fstypes.Stat
	dgst digest.Digest
}

func (fi *hostFileInfo) Name() string       { return path.Base(fi.stat.Path) }
func (fi *hostFileInfo) Size() int64        { return fi.stat.Size_ }
func (fi *hostFileInfo) Mode() os.FileMode  { return os.FileMode(fi.stat.Mode) }
func (fi *hostFileInfo) ModTime() time.Time { return time.Unix(0, fi.stat.ModTime) }
func (fi *hostFileInfo) IsDir() bool        { return fi.Mode().IsDir() }
func (fi *hostFileInfo) Sys() any           { return fi.stat }

func (fi *hostFileInfo) Digest() digest.Digest { return fi.dgst }

func xxh3Digest(s string) digest.Digest {
	return digest.NewDigestFromEncoded(hashutil.XXH3, fmt.Sprintf("%016x", xxh3.HashString(s)))
}

// hostSync builds a snapshot of files as a host import does: the files are
// written, and its tree is made from the XXH3 digests the client sends,
// rather than by a scan, which gives SHA-256 ones.
func hostSync(t *testing.T, store *testutil.Store, files map[string]string) bkcache.ImmutableRef {
	t.Helper()
	ctx := context.Background()
	ref, _ := store.BuildWith(t, nil, func(root string) {
		for name, data := range files {
			p := filepath.Join(root, name)
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(data), 0o644))
		}
	})
	md := ref.(bkcache.RefMetadata)
	cc, err := contenthash.GetCacheContext(ctx, md)
	require.NoError(t, err)
	dirs := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		p := "/" + name
		if dir := path.Dir(p); !dirs[dir] {
			dirs[dir] = true
			stat := &fstypes.Stat{Path: dir, Mode: uint32(os.ModeDir | 0o755)}
			require.NoError(t, cc.HandleChange(fsutil.ChangeKindAdd, dir, &hostFileInfo{stat, xxh3Digest(dir)}, nil))
		}
		stat := &fstypes.Stat{Path: p, Mode: 0o644, Size_: int64(len(files[name]))}
		require.NoError(t, cc.HandleChange(fsutil.ChangeKindAdd, p, &hostFileInfo{stat, xxh3Digest(files[name])}, nil))
	}
	require.NoError(t, contenthash.SetCacheContext(ctx, md, cc))
	// The directories' digests are computed by the first checksum, and saved
	// in the background.
	synced, err := md.GetExternal(bkcache.ContentHashMetadataKey)
	require.NoError(t, err)
	_, err = contenthash.Checksum(ctx, ref, "/", contenthash.ChecksumOpts{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		tree, err := md.GetExternal(bkcache.ContentHashMetadataKey)
		return err == nil && !bytes.Equal(tree, synced)
	}, 10*time.Second, time.Millisecond)
	return ref
}

// A tree kept on disk gives the digests a tree kept in memory gives, before
// and after a checkpoint and restart: the cache context that parsed it is
// dropped, and the next checksum reads the tree back. The host-synced
// snapshot's digests come from its tree's XXH3 records, where a rescan gives
// SHA-256 ones, so they only survive if the tree is read back.
func TestContentHashFilesRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewStore(t)
	refs := buildEdits(t, store)
	synced := map[string]string{
		"pkg03/file153.go": "package p // synced 153",
		"pkg03/file154.go": "package p // synced 154",
		"pkg07/file357.go": "package p // synced 357",
	}
	refs = append(refs, hostSync(t, store, synced))
	scanned, _ := store.BuildWith(t, nil, func(root string) {
		for name, data := range synced {
			p := filepath.Join(root, name)
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(data), 0o644))
		}
	})
	hashRoot(t, scanned)
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
	// A rescan of the synced files gives other digests.
	for _, p := range paths {
		dgst, err := contenthash.Checksum(ctx, scanned, p, contenthash.ChecksumOpts{})
		require.NoError(t, err)
		require.NotEqual(t, want[len(refs)-1][p], dgst, "rescanned digest of %s", p)
	}

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
	require.Equal(t, len(refs)+1, reloaded.ContentHashFiles, "the trees of refs and scanned")
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
