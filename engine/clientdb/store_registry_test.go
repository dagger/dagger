package clientdb

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreRegistryRefCount(t *testing.T) {
	registry := NewDBs(t.TempDir())

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

func TestStoreRegistryReopensPersistedStore(t *testing.T) {
	registry := NewDBs(t.TempDir())
	store, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)
	_, err = store.AppendMetrics([]Metric{{Data: []byte("retained")}})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	requireStoreStreamsClosed(t, store)
	require.Empty(t, registry.open)

	reopened, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)
	require.NotSame(t, store, reopened)
	rows, err := reopened.SelectMetricsSince(t.Context(), SelectMetricsSinceParams{ID: 0, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []Metric{{ID: 1, Data: []byte("retained")}}, rows)
	require.NoError(t, reopened.Close())
	require.NoError(t, registry.Close())
}

func TestStoreRegistryClosePreventsOpen(t *testing.T) {
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

func TestStoreRegistryActiveHandlesReleasedAfterClose(t *testing.T) {
	registry := NewDBs(t.TempDir())
	first, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)
	t.Cleanup(func() {
		if !streamClosed(first.spans) {
			require.NoError(t, first.closeStreams())
		}
	})
	second, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)
	require.Same(t, first, second)

	require.NoError(t, registry.Close())
	require.False(t, streamClosed(first.spans))
	require.False(t, streamClosed(first.logs))
	require.False(t, streamClosed(first.metrics))
	_, err = first.AppendMetrics([]Metric{{Data: []byte("after shutdown")}})
	require.NoError(t, err)

	require.NoError(t, first.Close())
	require.Equal(t, 1, second.refCount)
	require.False(t, streamClosed(second.spans))
	require.False(t, streamClosed(second.logs))
	require.False(t, streamClosed(second.metrics))
	rows, err := second.SelectMetricsSince(t.Context(), SelectMetricsSinceParams{ID: 0, Limit: 1})
	require.NoError(t, err)
	require.Equal(t, []Metric{{ID: 1, Data: []byte("after shutdown")}}, rows)

	require.NoError(t, second.Close())
	requireStoreStreamsClosed(t, second)
	require.Empty(t, registry.open)
}

func TestStoreRegistryCloseWaitsForInflightOpen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := NewDBs(t.TempDir())
		opened := make(chan *DB, 1)
		finishOpen := make(chan struct{})
		releaseOpen := sync.OnceFunc(func() { close(finishOpen) })
		defer releaseOpen()
		registry.openStore = func(ctx context.Context, root, clientID string, budget int64) (*DB, error) {
			store, err := openStore(ctx, root, clientID, budget)
			if err != nil {
				return nil, err
			}
			opened <- store
			<-finishOpen
			return store, nil
		}
		type openResult struct {
			store *DB
			err   error
		}
		openDone := make(chan openResult, 1)
		go func() {
			store, err := registry.Open(t.Context(), "client")
			openDone <- openResult{store, err}
		}()
		var store *DB
		select {
		case store = <-opened:
		case result := <-openDone:
			t.Fatalf("Open returned before reaching the barrier: %v", result.err)
		}
		t.Cleanup(func() {
			if !streamClosed(store.spans) {
				require.NoError(t, store.closeStreams())
			}
		})
		closeDone := make(chan error, 1)
		go func() { closeDone <- registry.Close() }()

		// Wait until Close is blocked on openingCond and Open is blocked at
		// the barrier. This checks the ordering without scheduler sleeps.
		synctest.Wait()
		registry.mu.RLock()
		closed, opening := registry.closed, registry.opening
		registry.mu.RUnlock()
		require.True(t, closed)
		require.Equal(t, 1, opening)
		select {
		case err := <-closeDone:
			t.Fatalf("Close returned while Open was still in flight: %v", err)
		default:
		}
		require.False(t, streamClosed(store.spans))
		require.False(t, streamClosed(store.logs))
		require.False(t, streamClosed(store.metrics))

		releaseOpen()
		require.NoError(t, <-closeDone)
		// Close must wait for the newly opened streams to be closed, not just
		// for the opening counter to reach zero.
		requireStoreStreamsClosed(t, store)
		result := <-openDone
		require.Nil(t, result.store)
		require.ErrorIs(t, result.err, errDBsClosed)
		require.Empty(t, registry.open)
		require.Zero(t, registry.opening)
	})
}

func TestStoreRegistryConcurrentOpenClose(t *testing.T) {
	registry := NewDBs(t.TempDir())

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
	require.Empty(t, registry.open)
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

func TestStoreRegistryGCKeepsProtectedHistory(t *testing.T) {
	root := t.TempDir()
	registry := NewDBs(root)
	store, err := registry.Open(t.Context(), "idle")
	require.NoError(t, err)
	require.NoError(t, store.Close())
	require.NoError(t, setStoreFileTimes(root, "idle", time.Now().Add(-CollectGarbageAfter-time.Minute)))

	require.NoError(t, registry.GC(map[string]bool{"idle": true}))
	requireStoreFilesExist(t, root, "idle")
	require.Empty(t, registry.open)
	requireStoreStreamsClosed(t, store)

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

func requireStoreStreamsClosed(t *testing.T, store *DB) {
	t.Helper()
	require.True(t, streamClosed(store.spans))
	require.True(t, streamClosed(store.logs))
	require.True(t, streamClosed(store.metrics))
	for _, file := range []*os.File{store.spans.spill.file, store.logs.spill.file, store.metrics.spill.file} {
		_, err := file.Stat()
		require.ErrorIs(t, err, os.ErrClosed)
	}
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
