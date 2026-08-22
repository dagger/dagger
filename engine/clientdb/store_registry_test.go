package clientdb

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreRegistryRefCount(t *testing.T) {
	registry := NewDBs(t.TempDir())
	registry.idleLimit = 0

	d1a, err := registry.Open(t.Context(), "client1")
	require.NoError(t, err)
	require.Len(t, registry.open, 1)
	require.Equal(t, 1, d1a.refCount)

	d1b, err := registry.Open(t.Context(), "client1")
	require.NoError(t, err)
	require.Same(t, d1a, d1b)
	require.Len(t, registry.open, 1)
	require.Equal(t, 2, d1a.refCount)
	require.Equal(t, OpenStats{Stores: 1, Streams: 3, Refs: 2}, registry.OpenStats())

	_, err = d1a.Read().SelectSpansSince(t.Context(), SelectSpansSinceParams{ID: 1, Limit: 1})
	require.NoError(t, err)

	require.NoError(t, d1a.Close())
	require.Len(t, registry.open, 1)
	require.Equal(t, 1, d1a.refCount)
	require.False(t, streamClosed(d1a.spans))
	require.False(t, streamClosed(d1a.logs))
	require.False(t, streamClosed(d1a.metrics))

	_, err = d1b.Read().SelectSpansSince(t.Context(), SelectSpansSinceParams{ID: 1, Limit: 1})
	require.NoError(t, err)

	d2, err := registry.Open(t.Context(), "client2")
	require.NoError(t, err)
	require.Len(t, registry.open, 2)
	require.Equal(t, 1, d2.refCount)

	require.NoError(t, d1b.Close())
	require.Len(t, registry.open, 1)
	require.Equal(t, 0, d1a.refCount)
	require.True(t, streamClosed(d1a.spans))
	require.True(t, streamClosed(d1a.logs))
	require.True(t, streamClosed(d1a.metrics))

	_, err = d2.Read().SelectSpansSince(t.Context(), SelectSpansSinceParams{ID: 1, Limit: 1})
	require.NoError(t, err)
	require.NoError(t, d2.Close())
	require.Empty(t, registry.open)
	require.Equal(t, 0, d2.refCount)
}

func TestStoreRegistryIdleReuse(t *testing.T) {
	registry := NewDBs(t.TempDir())
	registry.idleLimit = 2

	store, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)
	_, err = store.AppendMetrics([]Metric{{Data: []byte("retained")}})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	require.False(t, streamClosed(store.metrics))
	require.Equal(t, OpenStats{Stores: 1, Streams: 3}, registry.OpenStats())

	reused, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)
	require.Same(t, store, reused)
	rows, err := reused.SelectMetricsSince(t.Context(), SelectMetricsSinceParams{ID: 0, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []Metric{{ID: 1, Data: []byte("retained")}}, rows)
	require.NoError(t, reused.Close())
	require.NoError(t, registry.Close())
}

func TestStoreRegistryIdleLRUEviction(t *testing.T) {
	registry := NewDBs(t.TempDir())
	registry.idleLimit = 2

	first, err := registry.Open(t.Context(), "first")
	require.NoError(t, err)
	require.NoError(t, first.Close())
	second, err := registry.Open(t.Context(), "second")
	require.NoError(t, err)
	require.NoError(t, second.Close())

	// Reusing first makes second the least-recently-used idle store.
	refreshed, err := registry.Open(t.Context(), "first")
	require.NoError(t, err)
	require.Same(t, first, refreshed)
	require.NoError(t, refreshed.Close())

	third, err := registry.Open(t.Context(), "third")
	require.NoError(t, err)
	require.NoError(t, third.Close())

	require.False(t, streamClosed(first.spans))
	require.True(t, streamClosed(second.spans))
	require.False(t, streamClosed(third.spans))
	require.Len(t, registry.open, 2)
	require.Same(t, first, registry.open["first"])
	require.Same(t, third, registry.open["third"])
	require.NoError(t, registry.Close())
}

func TestStoreRegistryIdleEvictionLeavesActiveStoresOpen(t *testing.T) {
	registry := NewDBs(t.TempDir())
	registry.idleLimit = 1

	active, err := registry.Open(t.Context(), "active")
	require.NoError(t, err)
	oldIdle, err := registry.Open(t.Context(), "old-idle")
	require.NoError(t, err)
	require.NoError(t, oldIdle.Close())
	newIdle, err := registry.Open(t.Context(), "new-idle")
	require.NoError(t, err)
	require.NoError(t, newIdle.Close())

	require.True(t, streamClosed(oldIdle.spans))
	require.False(t, streamClosed(newIdle.spans))
	require.False(t, streamClosed(active.spans))
	require.Equal(t, 1, active.refCount)
	require.Equal(t, OpenStats{Stores: 2, Streams: 6, Refs: 1}, registry.OpenStats())

	require.NoError(t, active.Close())
	require.False(t, streamClosed(active.spans))
	require.True(t, streamClosed(newIdle.spans))
	require.NoError(t, registry.Close())
}

func TestStoreRegistryCloseCleansIdleStores(t *testing.T) {
	registry := NewDBs(t.TempDir())
	store, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)
	require.NoError(t, store.Close())

	require.NoError(t, registry.Close())
	require.True(t, streamClosed(store.spans))
	require.True(t, streamClosed(store.logs))
	require.True(t, streamClosed(store.metrics))
	require.Empty(t, registry.open)
	require.NoError(t, registry.Close())
	_, err = registry.Open(t.Context(), "other")
	require.ErrorIs(t, err, errDBsClosed)
}

func TestStoreRegistryConcurrentOpenClose(t *testing.T) {
	registry := NewDBs(t.TempDir())
	registry.idleLimit = 2

	const workers = 12
	start := make(chan struct{})
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for worker := range workers {
		group.Go(func() {
			<-start
			for iteration := 0; iteration < 50; iteration++ {
				clientID := "shared"
				if worker%2 == 0 {
					clientID = string(rune('a' + worker))
				}
				store, err := registry.Open(t.Context(), clientID)
				if err != nil {
					errs <- err
					return
				}
				if err := store.Close(); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.LessOrEqual(t, registry.idle.Len(), registry.idleLimit)
	require.NoError(t, registry.Close())
}

func TestStoreRegistryCloseNil(t *testing.T) {
	var store *DB
	require.NoError(t, store.Close())
}

func TestStoreRegistryOpenWithNonDirectoryRoot(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("nope"), 0o600))

	registry := NewDBs(blocker)
	_, err := registry.Open(t.Context(), "client1")
	require.Error(t, err)
	require.Empty(t, registry.open)
}

func TestStoreRegistryGCClosesIdleCachedStore(t *testing.T) {
	root := t.TempDir()
	registry := NewDBs(root)
	store, err := registry.Open(t.Context(), "idle")
	require.NoError(t, err)
	require.NoError(t, store.Close())
	require.NoError(t, setStoreFileTimes(root, "idle", time.Now().Add(-CollectGarbageAfter-time.Minute)))

	require.NoError(t, registry.GC(map[string]bool{"idle": true}))
	requireStoreFilesExist(t, root, "idle")
	require.Same(t, store, registry.open["idle"])
	require.False(t, streamClosed(store.spans))

	require.NoError(t, registry.GC(nil))
	requireStoreFilesMissing(t, root, "idle")
	require.NotContains(t, registry.open, "idle")
	require.True(t, streamClosed(store.spans))
	require.True(t, streamClosed(store.logs))
	require.True(t, streamClosed(store.metrics))
	require.NoError(t, registry.Close())
}

func TestStoreRegistryGC(t *testing.T) {
	root := t.TempDir()
	registry := NewDBs(root)
	registry.idleLimit = 0
	old := time.Now().Add(-CollectGarbageAfter - time.Minute)

	openStore, err := registry.Open(t.Context(), "open")
	require.NoError(t, err)
	require.NoError(t, setStoreFileTimes(root, "open", old))
	require.NoError(t, registry.GC(nil))
	requireStoreFilesExist(t, root, "open")
	require.NoError(t, openStore.Close())

	require.NoError(t, registry.GC(map[string]bool{"open": true}))
	requireStoreFilesExist(t, root, "open")
	require.NoError(t, registry.GC(nil))
	requireStoreFilesMissing(t, root, "open")

	fresh, err := registry.Open(t.Context(), "fresh")
	require.NoError(t, err)
	require.NoError(t, fresh.Close())
	require.NoError(t, setStoreFileTimes(root, "fresh", old))
	require.NoError(t, os.Chtimes(filepath.Join(root, "fresh.spans.log"), time.Now(), time.Now()))
	require.NoError(t, registry.GC(nil))
	// One fresh stream keeps the whole client store replayable.
	requireStoreFilesExist(t, root, "fresh")

	for _, name := range []string{"legacy.db", "legacy.db-wal", "legacy.db-shm"} {
		path := filepath.Join(root, name)
		require.NoError(t, os.WriteFile(path, []byte("legacy"), 0o600))
		require.NoError(t, os.Chtimes(path, old, old))
	}
	unrelated := filepath.Join(root, "unrelated.txt")
	require.NoError(t, os.WriteFile(unrelated, []byte("keep"), 0o600))
	require.NoError(t, os.Chtimes(unrelated, old, old))
	require.NoError(t, registry.GC(nil))
	for _, name := range []string{"legacy.db", "legacy.db-wal", "legacy.db-shm"} {
		_, err := os.Stat(filepath.Join(root, name))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	_, err = os.Stat(unrelated)
	require.NoError(t, err)
}

func streamClosed[Row any](stream *logStream[Row]) bool {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.closed
}

func storeFilePaths(root, clientID string) []string {
	return []string{
		filepath.Join(root, clientID+".spans.log"),
		filepath.Join(root, clientID+".logs.log"),
		filepath.Join(root, clientID+".metrics.log"),
	}
}

func setStoreFileTimes(root, clientID string, modTime time.Time) error {
	for _, path := range storeFilePaths(root, clientID) {
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			return err
		}
	}
	return nil
}

func requireStoreFilesExist(t *testing.T, root, clientID string) {
	t.Helper()
	for _, path := range storeFilePaths(root, clientID) {
		_, err := os.Stat(path)
		require.NoError(t, err)
	}
}

func requireStoreFilesMissing(t *testing.T, root, clientID string) {
	t.Helper()
	for _, path := range storeFilePaths(root, clientID) {
		_, err := os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}
