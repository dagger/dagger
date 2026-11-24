package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// An engine image's entries can carry another engine's row: its recipe and
// its content digest, without its contents. When the engine that merges them
// holds a result with that content digest, made by another recipe, the row
// takes its contents from that result instead of running its own recipe.
func TestMergedRowTakesContentsFromEquivalent(t *testing.T) {
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	actx, a, asrv := transferCache(t, aStore, filepath.Join(t.TempDir(), "a.db"), "a")
	observed := &partObservedManager{SnapshotManager: bStore.Manager}
	bStore.Manager = observed
	bctx, b, bsrv := transferCache(t, bStore, filepath.Join(t.TempDir(), "b.db"), "b")
	platform := Platform{OS: "linux", Architecture: "amd64"}
	contentDigest := digest.FromString("one file's contents")

	// The exporting engine's file, keyed by its content, exported as a recipe
	// without contents.
	produced := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: platform,
		Lazy: &FileBlobLazy{LazyState: NewLazyState(), Filename: "produced.txt", Contents: []byte("recipe bytes"), Permissions: 0o644}}
	original := attachTransferObject(t, actx, a, asrv, "a", "producedFile", produced)
	original, err := original.WithContentDigest(actx, contentDigest)
	require.NoError(t, err)
	var merged dagql.ObjectResult[*File]
	require.NoError(t, a.WithExportedValues(actx, dagql.ValueSelection{Roots: []dagql.AnyResult{original}}, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(ctx context.Context, exported *dagql.ExportedValues) error {
		reply, err := b.MergeValues(bctx, dagql.ImageCacheID, exported.Bundle)
		require.NoError(t, err)
		imported := reply.Imported()
		require.Len(t, imported, 1)
		loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
		require.NoError(t, err)
		merged = loaded.(dagql.ObjectResult[*File])
		return nil
	}))

	// The merging engine then makes a file with that content digest by
	// another recipe.
	ref, _ := bStore.Build(t, nil, "held/file.txt", "held bytes")
	held := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: platform}
	held.SetPath("/held/file.txt")
	held.SetSnapshot(ref)
	heldResult := attachTransferObject(t, bctx, b, bsrv, "b", "heldFile", held)
	heldResult, err = heldResult.WithContentDigest(bctx, contentDigest)
	require.NoError(t, err)
	mergedID, err := merged.ID()
	require.NoError(t, err)
	heldID, err := heldResult.ID()
	require.NoError(t, err)
	require.NotEqual(t, heldID.EngineResultID(), mergedID.EngineResultID(), "the two recipes are one entry, so nothing is taken from an equivalent")

	observed.bodies.Store(0)
	require.NoError(t, b.Evaluate(bctx, merged))
	require.Zero(t, observed.bodies.Load(), "the merged row ran its own recipe")
	require.Equal(t, "held bytes", string(demandedFileContents(t, bctx, merged)))
}
