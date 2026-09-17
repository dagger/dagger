package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

// An imported backing row arrives with no snapshot and creates one at first
// use. The row must own it: the session that created it ends, a real collection
// runs, and the snapshot is still mountable; then a clean restart keeps the
// cache instead of finding a saved link to a removed snapshot.
func TestImportedBackingSnapshotIsOwnedByItsRow(t *testing.T) {
	for _, kind := range []string{"cache volume", "git mirror", "filesync mirror"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			install := func(srv *dagql.Server) {
				srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*CacheVolume]{}))
				srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*RemoteGitMirror]{}))
				srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ClientFilesyncMirror]{}))
			}
			aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
			aCtx, a, aSrv := transferCache(t, aStore, "", "a")
			install(aSrv)
			aQuery, err := CurrentQuery(aCtx)
			require.NoError(t, err)
			var root dagql.AnyResult
			switch kind {
			case "cache volume":
				vol := NewCache("k", "ns", dagql.Null[dagql.ObjectResult[*Directory]](), CacheSharingModeShared, "")
				require.NoError(t, vol.InitializeSnapshot(aCtx))
				root = attachTransferObject(t, aCtx, a, aSrv, "a", "vol", vol)
			case "git mirror":
				mirror := NewRemoteGitMirror("https://example.invalid/repo.git")
				require.NoError(t, mirror.EnsureCreated(aCtx, aQuery))
				root = attachTransferObject(t, aCtx, a, aSrv, "a", "mirror", mirror)
			case "filesync mirror":
				mirror := &ClientFilesyncMirror{StableClientID: "stable"}
				require.NoError(t, mirror.EnsureCreated(aCtx, aQuery))
				root = attachTransferObject(t, aCtx, a, aSrv, "a", "filesync", mirror)
			}
			var bundle dagql.ValueBundle
			require.NoError(t, a.WithExportedValues(aCtx, dagql.ValueSelection{Roots: []dagql.AnyResult{root}}, config.RefConfig{}, func(_ context.Context, v *dagql.ExportedValues) error {
				bundle = v.Bundle
				return nil
			}))

			// use does what the production sites do with the row, then mounts
			// the snapshot and writes through it.
			use := func(ctx context.Context, row dagql.AnyResult) string {
				var ref bkcache.MutableRef
				switch row := row.(type) {
				case dagql.ObjectResult[*CacheVolume]:
					_, err := (&Container{}).WithMountedCache(ctx, "/cache", row)
					require.NoError(t, err)
					ref = row.Self().getSnapshot()
				case dagql.ObjectResult[*RemoteGitMirror]:
					require.NoError(t, EnsureBackingSnapshot(ctx, row))
					ref = row.Self().snapshot
				case dagql.ObjectResult[*ClientFilesyncMirror]:
					require.NoError(t, EnsureBackingSnapshot(ctx, row))
					ref = row.Self().snapshot
				default:
					t.Fatalf("unexpected row %T", row)
				}
				mountable, err := ref.Mount(ctx, false)
				require.NoError(t, err)
				mounts, release, err := mountable.Mount()
				require.NoError(t, err)
				require.Len(t, mounts, 1)
				require.NoError(t, os.WriteFile(filepath.Join(mounts[0].Source, "used"), []byte(kind), 0o600))
				if release != nil {
					require.NoError(t, release())
				}
				return ref.SnapshotID()
			}

			path := filepath.Join(t.TempDir(), "b.db")
			bCtx, b, bSrv := transferCache(t, bStore, path, "first")
			install(bSrv)
			imported, err := b.ImportValues(bCtx, bundle)
			require.NoError(t, err)
			rowID := imported[len(imported)-1].ResultID
			row, err := b.LoadResultByResultID(bCtx, "first", bSrv, rowID)
			require.NoError(t, err)
			require.Empty(t, row.Unwrap().(interface {
				PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink
			}).PersistedSnapshotRefLinks(), "the imported row starts without a snapshot")
			snapshotID := use(bCtx, row)

			require.NoError(t, b.ReleaseSession(bCtx, "first"))
			bStore.GC(t)
			secondCtx := engine.ContextWithClientMetadata(bCtx, &engine.ClientMetadata{ClientID: "second", SessionID: "second"})
			again, err := b.LoadResultByResultID(secondCtx, "second", bSrv, rowID)
			require.NoError(t, err)
			require.Equal(t, snapshotID, use(secondCtx, again), "the live row still mounts its snapshot after the collection")
			require.NoError(t, b.ReleaseSession(secondCtx, "second"))

			bStore.GC(t)
			require.NoError(t, b.Close(bCtx))
			bStore.GC(t)
			bStore.Reload(t)
			cCtx, restarted, cSrv := transferCache(t, bStore, path, "third")
			install(cSrv)
			require.Equal(t, dagql.CachePersistenceResetNone, restarted.PersistenceResetReason())
			reopened, err := restarted.LoadResultByResultID(cCtx, "third", cSrv, rowID)
			require.NoError(t, err)
			require.Equal(t, snapshotID, use(cCtx, reopened), "the restarted row reopens the same snapshot")
		})
	}
}
