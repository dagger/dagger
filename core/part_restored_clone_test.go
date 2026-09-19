package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// sharedImportedRow imports root's row into a second cache that already has a
// complete local equivalent under the same recipe, lets a sharing pass install
// the imported row's snapshot while it is still encoded, and then loads that
// exact row. What comes back is how a part-acquired File or Directory looks
// once decoded: its stored descriptor and a restore operation that has not
// run, because a managed row's Evaluate opens the part instead of running it.
func sharedImportedRow[T dagql.Typed](t *testing.T, field string, build func(bkcache.ImmutableRef) T) (context.Context, dagql.ObjectResult[T]) {
	t.Helper()
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	aRef, _ := aStore.Build(t, nil, "notes.txt", "shared notes")
	aCtx, a, aSrv := transferCache(t, aStore, "", "a")
	root := attachTransferObject(t, aCtx, a, aSrv, "a", field, build(aRef))
	var bundle dagql.ValueBundle
	require.NoError(t, a.WithExportedValues(aCtx, dagql.ValueSelection{Roots: []dagql.AnyResult{root}}, config.RefConfig{}, func(_ context.Context, v *dagql.ExportedValues) error {
		bundle = v.Bundle
		return nil
	}))
	bCtx, b, bSrv := transferCache(t, bStore, filepath.Join(t.TempDir(), "b.db"), "b")
	require.NoError(t, b.EnableSnapshotSharing())
	b.EnableTransferFixtureParts()
	bRef, _ := bStore.Build(t, nil, "notes.txt", "shared notes")
	attachTransferObject(t, bCtx, b, bSrv, "b", field, build(bRef))
	synced, err := b.ArmTransferFixtureBarrier(dagql.FixtureBarrierRequest{Key: "synced", Point: dagql.FixtureOwnerSyncDone, Action: dagql.FixturePause})
	require.NoError(t, err)
	imported, err := b.ImportValues(bCtx, bundle)
	require.NoError(t, err)
	wait, cancel := context.WithTimeout(bCtx, 10*time.Second)
	defer cancel()
	_, err = b.WaitTransferFixtureBarrier(wait, synced.Key, synced.Generation)
	require.NoError(t, err, "the sharing pass never synchronized the imported row")
	require.NoError(t, b.ReleaseTransferFixtureBarrier(synced.Key, synced.Generation))
	loaded, err := b.LoadResultByResultID(bCtx, "", bSrv, imported[len(imported)-1].ResultID)
	require.NoError(t, err)
	row := loaded.(dagql.ObjectResult[T])
	require.NoError(t, b.Evaluate(bCtx, row))
	return bCtx, row
}

// A Container operation that takes an evaluated File or Directory clones it.
// The clone asked whether the value's operation had run. For a part-acquired
// value it never does, so mounting an imported File into a Container, which
// the retained-exec fallback does, failed with "file must be materialized,
// got lazy *core.FileRestoreLazy" although the File's output was installed.
// The question is whether computation is still pending, which a value with a
// stored descriptor answers no.
func TestPartAcquiredValuesCloneForContainers(t *testing.T) {
	platform := Platform{OS: "linux", Architecture: "amd64"}
	t.Run("File", func(t *testing.T) {
		ctx, row := sharedImportedRow(t, "sharedFile", func(ref bkcache.ImmutableRef) *File {
			f := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: platform}
			f.SetPath("/notes.txt")
			f.SetSnapshot(ref)
			return f
		})
		require.IsType(t, &FileRestoreLazy{}, row.Self().Lazy)
		require.False(t, row.Self().Lazy.IsEvaluated(), "the restore operation did not run: the part was opened")
		require.False(t, row.Self().HasPendingLazyComputation())
		clone, err := cloneDetachedFileForContainerResult(ctx, row.Self())
		require.NoError(t, err)
		body, _ := producedFileContents(t, clone)
		require.Equal(t, "shared notes", string(body))
		require.NoError(t, clone.OnRelease(ctx))
	})
	t.Run("Directory", func(t *testing.T) {
		ctx, row := sharedImportedRow(t, "sharedDir", func(ref bkcache.ImmutableRef) *Directory {
			d := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: platform}
			d.SetPath("/")
			d.SetSnapshot(ref)
			return d
		})
		require.IsType(t, &DirectoryRestoreLazy{}, row.Self().Lazy)
		require.False(t, row.Self().Lazy.IsEvaluated())
		clone, err := cloneDetachedDirectoryForContainerResult(ctx, row.Self())
		require.NoError(t, err)
		require.NoError(t, clone.OnRelease(ctx))
		_, path, err := materializedDirectorySnapshotAndPath(row.Self())
		require.NoError(t, err)
		require.Equal(t, "/", path)
	})
}
