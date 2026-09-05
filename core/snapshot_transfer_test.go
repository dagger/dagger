package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

func transferCache(t *testing.T, store *testutil.Store, path, session string) (context.Context, *dagql.Cache, *dagql.Server) {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: session, SessionID: session})
	cache, err := dagql.NewCache(ctx, path, store.Manager, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	query := &Query{Server: &cacheVolumeTestQueryServer{mockServer: &mockServer{}, cacheManager: store.Manager}}
	ctx = ContextWithQuery(dagql.ContextWithCache(ctx, cache), query)
	srv := newCoreDagqlServerForTest(t, query)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Container]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*File]{}))
	return ctx, cache, srv
}

func attachTransferObject[T dagql.Typed](t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, session, field string, value T) dagql.ObjectResult[T] {
	t.Helper()
	call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(value.Type())}
	result, err := cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: call, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(value, srv, call)
	})
	require.NoError(t, err)
	return result.(dagql.ObjectResult[T])
}

func TestSnapshotTransferTypedAdoptionAndRestart(t *testing.T) {
	producer, consumer := testutil.NewStore(t), testutil.NewStore(t)
	a, _ := producer.Build(t, nil, "a.txt", "typed prefix")
	ab, _ := producer.Build(t, a, "dir/b.txt", "typed suffix")
	chain, err := ab.ExportChain(t.Context(), config.RefConfig{Compression: compression.New(compression.Uncompressed)})
	require.NoError(t, err)
	defer chain.Release(context.Background())
	provider := &testutil.Provider{InfoReaderProvider: chain.Provider}
	imported, err := consumer.Manager.ImportChain(t.Context(), &bkcache.ExportChain{Layers: chain.Layers, Provider: provider})
	require.NoError(t, err)
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := transferCache(t, consumer, dbPath, "before")
	platform := Platform{OS: "linux", Architecture: "amd64"}
	root := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: platform}
	root.Dir.setValue("/")
	root.Snapshot.setValue(imported)
	container := NewContainer(platform)
	container.FS.setValue(root)
	containerResult := attachTransferObject(t, ctx, cache, srv, "before", "transferredContainer", container)
	directory := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: platform,
		Lazy: &ContainerRootFSLazy{LazyState: NewLazyState(), Parent: containerResult}}
	directoryResult := attachTransferObject(t, ctx, cache, srv, "before", "transferredDirectory", directory)
	entries, err := directory.Entries(ctx, directoryResult, "")
	require.NoError(t, err)
	require.Contains(t, entries, "a.txt")
	opened, err := consumer.Manager.GetBySnapshotID(ctx, imported.SnapshotID())
	require.NoError(t, err)
	file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: platform}
	file.File.setValue("/dir/b.txt")
	file.Snapshot.setValue(opened)
	fileResult := attachTransferObject(t, ctx, cache, srv, "before", "transferredFile", file)
	contents, err := file.Contents(ctx, fileResult, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "typed suffix", string(contents))
	results := []dagql.AnyResult{containerResult, directoryResult, fileResult}
	ids := make([]uint64, len(results))
	for i, result := range results {
		ids[i], err = cache.PersistedResultID(result)
		require.NoError(t, err)
	}
	assertTypedSnapshotOwners(t, consumer, imported.SnapshotID(), 3)
	require.NoError(t, cache.ReleaseSession(ctx, "before"))
	require.NoError(t, cache.Close(ctx))
	remaining, err := consumer.Leases.List(context.Background())
	require.NoError(t, err)
	for _, owner := range remaining {
		resources, err := consumer.Leases.ListResources(context.Background(), owner)
		require.NoError(t, err)
		t.Logf("after clean cache close: lease=%s labels=%v resources=%v", owner.ID, owner.Labels, resources)
	}
	// Release any transfer pin still held by the original object. Persisted
	// result leases must protect the physical data across the next manager.
	require.NoError(t, imported.Release(context.Background()))
	consumer.GC(t)
	consumer.Reload(t)
	require.NoError(t, consumer.Manager.LoadPersistentMetadata(bkcache.PersistentMetadataRows{}))
	ctx, cache, srv = transferCache(t, consumer, dbPath, "after")
	reimported, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: provider})
	require.NoError(t, err)
	require.Equal(t, imported.SnapshotID(), reimported.SnapshotID())
	require.EqualValues(t, 2, provider.Reads.Load())
	require.EqualValues(t, 2, consumer.Applies.Load())
	require.NoError(t, reimported.Release(context.Background()))
	loaded := make([]dagql.AnyResult, len(ids))
	for i, id := range ids {
		loaded[i], err = cache.LoadResultByResultID(ctx, "after", srv, id)
		require.NoError(t, err)
	}
	loadedContainer := loaded[0].(dagql.ObjectResult[*Container])
	require.NoError(t, cache.EvaluateParts(ctx, loadedContainer, ContainerPartFS))
	restoredRoot, err := loadedContainer.Self().FS.GetOrEval(ctx, loadedContainer.Result)
	require.NoError(t, err)
	restoredSnapshot, ok := restoredRoot.Snapshot.Peek()
	require.True(t, ok)
	testutil.CheckFile(t, restoredSnapshot, "a.txt", "typed prefix")
	loadedDirectory := loaded[1].(dagql.ObjectResult[*Directory])
	entries, err = loadedDirectory.Self().Entries(ctx, loadedDirectory, "dir")
	require.NoError(t, err)
	require.Equal(t, []string{"b.txt"}, entries)
	loadedFile := loaded[2].(dagql.ObjectResult[*File])
	contents, err = loadedFile.Self().Contents(ctx, loadedFile, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "typed suffix", string(contents))
	assertTypedSnapshotOwners(t, consumer, imported.SnapshotID(), 3)
	require.EqualValues(t, 2, provider.Reads.Load())
	require.EqualValues(t, 2, consumer.Applies.Load())
	require.NoError(t, cache.ReleaseSession(ctx, "after"))
	require.NoError(t, cache.Close(ctx))
	t.Logf("Container, Directory and File persisted/reopened: source reads=%d apply calls=%d", provider.Reads.Load(), consumer.Applies.Load())
}

func assertTypedSnapshotOwners(t *testing.T, store *testutil.Store, id string, want int) {
	t.Helper()
	owners, err := store.Leases.List(context.Background())
	require.NoError(t, err)
	var count int
	for _, owner := range owners {
		if !strings.HasPrefix(owner.ID, "dagql/result/") {
			continue
		}
		resources, err := store.Leases.ListResources(context.Background(), owner)
		require.NoError(t, err)
		for _, resource := range resources {
			if resource == (leases.Resource{ID: id, Type: "snapshots/native"}) {
				count++
			}
		}
	}
	require.Equal(t, want, count)
}
