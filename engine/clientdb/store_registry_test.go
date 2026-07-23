package clientdb

import (
	"os"
	"path/filepath"
	"testing"
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
