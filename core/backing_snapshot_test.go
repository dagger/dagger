package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
)

var backingKinds = []string{"cache volume", "git mirror", "filesync mirror"}

// importedBacking is one backing row of the given kind, exported by A and
// imported into B's persistent cache, not yet used on B.
type importedBacking struct {
	kind  string
	store *testutil.Store
	path  string
	rowID uint64
	ctx   context.Context
	cache *dagql.Cache
	srv   *dagql.Server
}

func installBackingClasses(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*CacheVolume]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*RemoteGitMirror]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ClientFilesyncMirror]{}))
}

func newImportedBacking(t *testing.T, kind string) *importedBacking {
	t.Helper()
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	aCtx, a, aSrv := transferCache(t, aStore, "", "a")
	installBackingClasses(aSrv)
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

	path := filepath.Join(t.TempDir(), "b.db")
	bCtx, b, bSrv := transferCache(t, bStore, path, "first")
	installBackingClasses(bSrv)
	imported, err := b.ImportValues(bCtx, bundle)
	require.NoError(t, err)
	return &importedBacking{kind: kind, store: bStore, path: path, rowID: imported[len(imported)-1].ResultID, ctx: bCtx, cache: b, srv: bSrv}
}

func (ib *importedBacking) session(name string) context.Context {
	return engine.ContextWithClientMetadata(ib.ctx, &engine.ClientMetadata{ClientID: name, SessionID: name})
}

func (ib *importedBacking) load(t *testing.T, ctx context.Context, session string) dagql.AnyResult {
	t.Helper()
	row, err := ib.cache.LoadResultByResultID(ctx, session, ib.srv, ib.rowID)
	require.NoError(t, err)
	return row
}

func backingLinks(t *testing.T, row dagql.AnyResult) []dagql.PersistedSnapshotRefLink {
	t.Helper()
	return row.Unwrap().(interface {
		PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink
	}).PersistedSnapshotRefLinks()
}

// ensure does what the production sites do with the row: the cache volume
// through WithMountedCache, the mirrors through the helper their sites call.
func (ib *importedBacking) ensure(ctx context.Context, row dagql.AnyResult) (bkcache.MutableRef, error) {
	switch row := row.(type) {
	case dagql.ObjectResult[*CacheVolume]:
		_, err := (&Container{}).WithMountedCache(ctx, "/cache", row)
		return row.Self().getSnapshot(), err
	case dagql.ObjectResult[*RemoteGitMirror]:
		err := EnsureBackingSnapshot(ctx, row)
		row.Self().mu.Lock()
		defer row.Self().mu.Unlock()
		return row.Self().snapshot, err
	case dagql.ObjectResult[*ClientFilesyncMirror]:
		err := EnsureBackingSnapshot(ctx, row)
		row.Self().mu.Lock()
		defer row.Self().mu.Unlock()
		return row.Self().snapshot, err
	}
	panic("unexpected row")
}

// use ensures the snapshot, mounts it and writes through it.
func (ib *importedBacking) use(t *testing.T, ctx context.Context, row dagql.AnyResult) string {
	t.Helper()
	ref, err := ib.ensure(ctx, row)
	require.NoError(t, err)
	mountable, err := ref.Mount(ctx, false)
	require.NoError(t, err)
	mounts, release, err := mountable.Mount()
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	require.NoError(t, os.WriteFile(filepath.Join(mounts[0].Source, "used"), []byte(ib.kind), 0o600))
	if release != nil {
		require.NoError(t, release())
	}
	return ref.SnapshotID()
}

// restart closes B cleanly, collects, reloads the store and reopens the cache,
// which must keep its persistence.
func (ib *importedBacking) restart(t *testing.T) (context.Context, *dagql.Cache, *dagql.Server) {
	t.Helper()
	require.NoError(t, ib.cache.Close(ib.ctx))
	ib.store.GC(t)
	ib.store.Reload(t)
	ctx, restarted, srv := transferCache(t, ib.store, ib.path, "restarted")
	installBackingClasses(srv)
	require.Equal(t, dagql.CachePersistenceResetNone, restarted.PersistenceResetReason())
	ib.ctx, ib.cache, ib.srv = ctx, restarted, srv
	return ctx, restarted, srv
}

// An imported backing row arrives with no snapshot and creates one at first
// use. The row must own it: the session that created it ends, a real collection
// runs, and the snapshot is still mountable; then a clean restart keeps the
// cache instead of finding a saved link to a removed snapshot.
func TestImportedBackingSnapshotIsOwnedByItsRow(t *testing.T) {
	for _, kind := range backingKinds {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ib := newImportedBacking(t, kind)
			row := ib.load(t, ib.ctx, "first")
			require.Empty(t, backingLinks(t, row), "the imported row starts without a snapshot")
			snapshotID := ib.use(t, ib.ctx, row)

			require.NoError(t, ib.cache.ReleaseSession(ib.ctx, "first"))
			ib.store.GC(t)
			second := ib.session("second")
			require.Equal(t, snapshotID, ib.use(t, second, ib.load(t, second, "second")), "the live row still mounts its snapshot after the collection")
			require.NoError(t, ib.cache.ReleaseSession(second, "second"))
			ib.store.GC(t)

			ctx, _, _ := ib.restart(t)
			require.Equal(t, snapshotID, ib.use(t, ctx, ib.load(t, ctx, "restarted")), "the restarted row reopens the same snapshot")
		})
	}
}

// When the owner lease cannot be attached at first use, the call fails and the
// snapshot it created is dropped with it, so the value reports no link. The
// session ends and a collection runs before any retry; the retry in a later
// session creates and owns a new snapshot, and a restart without any retry
// keeps the cache, because nothing dangling was saved.
func TestImportedBackingSnapshotIsDroppedWhenItsOwnerAttachFails(t *testing.T) {
	for _, kind := range backingKinds {
		for _, retry := range []string{"retry after the collection", "restart without a retry"} {
			t.Run(kind+"/"+retry, func(t *testing.T) {
				t.Parallel()
				ib := newImportedBacking(t, kind)
				ib.cache.EnableTransferFixtureParts()
				row := ib.load(t, ib.ctx, "first")
				armed, err := ib.cache.ArmTransferFixtureBarrier(dagql.FixtureBarrierRequest{
					Key:      "attach",
					Point:    dagql.FixtureBeforeOwnerAttach,
					Selector: dagql.FixtureBarrierSelector{ResultID: ib.rowID},
					Action:   dagql.FixtureFailOwnerAttachBefore,
				})
				require.NoError(t, err)
				_, err = ib.ensure(ib.ctx, row)
				require.ErrorContains(t, err, "fault", "the first use fails with the attach")
				wait, cancel := context.WithTimeout(ib.ctx, 10*time.Second)
				defer cancel()
				_, err = ib.cache.WaitTransferFixtureBarrier(wait, armed.Key, armed.Generation)
				require.NoError(t, err)
				require.Empty(t, backingLinks(t, row), "the failed use leaves no snapshot behind")

				require.NoError(t, ib.cache.ReleaseSession(ib.ctx, "first"))
				ib.store.GC(t)

				if retry == "retry after the collection" {
					second := ib.session("second")
					snapshotID := ib.use(t, second, ib.load(t, second, "second"))
					require.NoError(t, ib.cache.ReleaseSession(second, "second"))
					ib.store.GC(t)
					ctx, _, _ := ib.restart(t)
					require.Equal(t, snapshotID, ib.use(t, ctx, ib.load(t, ctx, "restarted")), "the restarted row reopens the retried snapshot")
					return
				}
				ctx, _, _ := ib.restart(t)
				restartedRow := ib.load(t, ctx, "restarted")
				require.Empty(t, backingLinks(t, restartedRow), "the restarted row is still uninitialised")
				ib.use(t, ctx, restartedRow)
			})
		}
	}
}

// Concurrent first uses of one row are one step each: with the first attach
// failing, exactly that caller fails, every other caller gets a snapshot that
// is still there to mount, and the row ends owning one snapshot.
func TestImportedBackingSnapshotConcurrentFirstUses(t *testing.T) {
	for _, kind := range backingKinds {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ib := newImportedBacking(t, kind)
			ib.cache.EnableTransferFixtureParts()
			_, err := ib.cache.ArmTransferFixtureBarrier(dagql.FixtureBarrierRequest{
				Key:      "attach",
				Point:    dagql.FixtureBeforeOwnerAttach,
				Selector: dagql.FixtureBarrierSelector{ResultID: ib.rowID},
				Action:   dagql.FixtureFailOwnerAttachBefore,
			})
			require.NoError(t, err)
			const callers = 8
			errs := make([]error, callers)
			ids := make([]string, callers)
			var wg sync.WaitGroup
			for i := range callers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					session := fmt.Sprintf("caller-%d", i)
					ctx := ib.session(session)
					row, err := ib.cache.LoadResultByResultID(ctx, session, ib.srv, ib.rowID)
					if err != nil {
						errs[i] = err
						return
					}
					ref, err := ib.ensure(ctx, row)
					if err != nil {
						errs[i] = err
						return
					}
					if ref == nil {
						errs[i] = fmt.Errorf("a successful use has no snapshot")
						return
					}
					mountable, err := ref.Mount(ctx, false)
					if err != nil {
						errs[i] = err
						return
					}
					mounts, release, err := mountable.Mount()
					if err != nil {
						errs[i] = err
						return
					}
					if _, err := os.Stat(mounts[0].Source); err != nil {
						errs[i] = err
					}
					if release != nil {
						_ = release()
					}
					ids[i] = ref.SnapshotID()
				}()
			}
			wg.Wait()
			failed := 0
			for i, err := range errs {
				if err != nil {
					require.ErrorContains(t, err, "fault", "caller %d fails only with the injected fault", i)
					failed++
				}
			}
			require.Equal(t, 1, failed, "exactly the caller whose attach was faulted fails")
			want := ""
			for i, id := range ids {
				if errs[i] != nil {
					continue
				}
				if want == "" {
					want = id
				}
				require.Equal(t, want, id, "every successful caller uses the one snapshot")
			}
			links := backingLinks(t, ib.load(t, ib.session("last"), "last"))
			require.Len(t, links, 1)
			require.Equal(t, want, links[0].RefKey)
		})
	}
}
