package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// A copy of a directory is keyed by the hash of the files under its root
// (copySourceDigest in core/schema). So a directory an engine restores after a
// restart, or merges from another engine's export, keeps the hash it was made
// with: a copy of it is then keyed there as on the engine that made it.
func TestDirectoryFilesDigestTravelsWithTheDirectory(t *testing.T) {
	const files = digest.Digest("sha256:4f2a7c1e9b3d5f6a8c0e2d4b6a8f0c2e4d6b8a0f2c4e6d8b0a2f4c6e8d0b2a4f")

	t.Run("restart", func(t *testing.T) {
		env := newPersistedFamiliesTestEnv(t, "files-digest")
		ctx, cache, srv := env.open(t)
		dir := containerPersistenceTestDirectory("source", "/pkg")
		dir.SetFilesDigest(files)
		res := env.attach(t, ctx, cache, srv, "subpathRead", dir)
		rec, err := cache.CapturePersistedRecord(ctx, res)
		require.NoError(t, err)
		ctx, cache, srv = env.restart(t, ctx, cache)
		loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, rec.ResultID)
		require.NoError(t, err)
		require.Equal(t, files, loaded.Unwrap().(*Directory).FilesDigest())
	})

	t.Run("another engine", func(t *testing.T) {
		producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
		tree, _ := producer.Build(t, nil, "pkg/a.txt", "a")
		ctx, a, srv := transferCache(t, producer, filepath.Join(t.TempDir(), "a.db"), "a")
		dir := partTestDirectory(tree, "/pkg")
		dir.SetFilesDigest(files)
		res := attachTransferObject(t, ctx, a, srv, "a", "subpathRead", dir)
		var bundle dagql.ValueBundle
		require.NoError(t, a.WithExportedValues(ctx, dagql.ValueSelection{Roots: []dagql.AnyResult{res}}, config.RefConfig{}, func(_ context.Context, ex *dagql.ExportedValues) error {
			bundle = ex.Bundle
			return nil
		}))
		bctx, b, bsrv := transferCache(t, consumer, filepath.Join(t.TempDir(), "b.db"), "b")
		reply, err := b.MergeValues(bctx, dagql.CloudCacheID, bundle)
		require.NoError(t, err)
		imported := reply.Imported()
		require.Len(t, imported, 1)
		loaded, err := b.LoadResultByResultID(bctx, "b", bsrv, imported[0].ResultID)
		require.NoError(t, err)
		require.Equal(t, files, loaded.(dagql.ObjectResult[*Directory]).Self().FilesDigest())
	})
}
