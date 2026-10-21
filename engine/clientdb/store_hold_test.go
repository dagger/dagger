package clientdb

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// countOpens makes registry count real (replaying) store opens.
func countOpens(registry *DBs) *atomic.Int32 {
	var opens atomic.Int32
	registry.openStore = func(ctx context.Context, root, clientID string, budget int64, pos *storePosition) (*DB, error) {
		opens.Add(1)
		return openStoreStreams(ctx, root, clientID, budget, pos)
	}
	return &opens
}

func TestStoreRegistryIdleHoldKeepsStoreUntilIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := NewDBs(t.TempDir())
		opens := countOpens(registry)

		store, err := registry.OpenHeld(t.Context(), "archive")
		require.NoError(t, err)
		require.NoError(t, store.Close())
		require.Equal(t, OpenStats{Stores: 1, Streams: 3, Refs: 1}, registry.OpenStats(), "the idle hold keeps the store open")

		// Uses within the idle period share the open store and refresh the hold.
		time.Sleep(registry.IdleHold - time.Second)
		again, err := registry.OpenHeld(t.Context(), "archive")
		require.NoError(t, err)
		require.Same(t, store, again)
		require.NoError(t, again.Close())
		require.EqualValues(t, 1, opens.Load())

		time.Sleep(registry.IdleHold - time.Second)
		synctest.Wait()
		require.Equal(t, 1, registry.OpenStats().Stores, "the last use refreshed the hold")

		time.Sleep(2 * time.Second)
		synctest.Wait()
		require.Equal(t, OpenStats{}, registry.OpenStats())
		requireStoreStreamsClosed(t, store)

		// A plain Open never takes a hold.
		plain, err := registry.Open(t.Context(), "archive")
		require.NoError(t, err)
		require.NoError(t, plain.Close())
		require.Equal(t, OpenStats{}, registry.OpenStats())
		require.NoError(t, registry.Close())
	})
}

func TestStoreRegistryIdleHoldOutlivesLongReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := NewDBs(t.TempDir())
		opens := countOpens(registry)
		reader, err := registry.OpenHeld(t.Context(), "archive")
		require.NoError(t, err)
		// The read outlasts IdleHold; the store is in use, not idle.
		time.Sleep(registry.IdleHold + time.Second)
		synctest.Wait()
		require.NoError(t, reader.Close())
		require.Equal(t, OpenStats{Stores: 1, Streams: 3, Refs: 1}, registry.OpenStats(), "the hold survives a long read")
		again, err := registry.OpenHeld(t.Context(), "archive")
		require.NoError(t, err)
		require.Same(t, reader, again)
		require.NoError(t, again.Close())
		require.EqualValues(t, 1, opens.Load())

		time.Sleep(2 * registry.IdleHold)
		synctest.Wait()
		require.Equal(t, OpenStats{}, registry.OpenStats(), "an idle store is released")
		require.NoError(t, registry.Close())
	})
}

func TestStoreRegistryIdleHoldNeverBlocksRemoval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		registry := NewDBs(root)

		store, err := registry.OpenHeld(t.Context(), "idle")
		require.NoError(t, err)
		require.NoError(t, store.Close())
		removed, err := registry.Remove("idle")
		require.NoError(t, err)
		require.True(t, removed, "an idle hold alone must not block removal")
		requireStoreFilesMissing(t, root, "idle")
		requireStoreStreamsClosed(t, store)
		require.Equal(t, OpenStats{}, registry.OpenStats())

		// An active reader still blocks removal, but the hold is dropped, so
		// the store closes with the reader and the next attempt succeeds.
		reader, err := registry.OpenHeld(t.Context(), "busy")
		require.NoError(t, err)
		removed, err = registry.Remove("busy")
		require.NoError(t, err)
		require.False(t, removed)
		require.Equal(t, OpenStats{Stores: 1, Streams: 3, Refs: 1}, registry.OpenStats())
		require.NoError(t, reader.Close())
		require.Equal(t, OpenStats{}, registry.OpenStats())
		removed, err = registry.Remove("busy")
		require.NoError(t, err)
		require.True(t, removed)

		// GC collects an old, unkept store despite its idle hold.
		gc, err := registry.OpenHeld(t.Context(), "gc")
		require.NoError(t, err)
		require.NoError(t, gc.Close())
		require.NoError(t, setStoreFileTimes(root, "gc", time.Now().Add(-CollectGarbageAfter-time.Minute)))
		require.NoError(t, registry.GC(map[string]bool{"gc": true}))
		require.Equal(t, 1, registry.OpenStats().Stores, "a kept store keeps its hold")
		require.NoError(t, registry.GC(nil))
		requireStoreFilesMissing(t, root, "gc")
		requireStoreStreamsClosed(t, gc)
		require.Equal(t, OpenStats{}, registry.OpenStats())

		// The expired hold's timer is a no-op once the hold is gone.
		time.Sleep(2 * registry.IdleHold)
		synctest.Wait()
		require.NoError(t, registry.Close())
	})
}

func TestStoreRegistryCloseReleasesIdleHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := NewDBs(t.TempDir())
		held, err := registry.OpenHeld(t.Context(), "held")
		require.NoError(t, err)
		require.NoError(t, held.Close())
		active, err := registry.OpenHeld(t.Context(), "active")
		require.NoError(t, err)

		require.NoError(t, registry.Close())
		requireStoreStreamsClosed(t, held)
		require.False(t, streamClosed(active.spans), "active handles outlive registry shutdown")
		require.NoError(t, active.Close())
		requireStoreStreamsClosed(t, active)
		require.Equal(t, OpenStats{}, registry.OpenStats())

		_, err = registry.OpenHeld(t.Context(), "held")
		require.ErrorIs(t, err, errDBsClosed)
		time.Sleep(2 * registry.IdleHold)
		synctest.Wait()
	})
}

func TestStoreRegistryIdleHoldsAreBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		registry := NewDBs(t.TempDir())
		registry.MaxIdleHolds = 2
		var stores []*DB
		for _, id := range []string{"a", "b", "c"} {
			store, err := registry.OpenHeld(t.Context(), id)
			require.NoError(t, err)
			require.NoError(t, store.Close())
			stores = append(stores, store)
			time.Sleep(time.Second)
		}
		require.Equal(t, 2, registry.OpenStats().Stores)
		requireStoreStreamsClosed(t, stores[0])
		require.False(t, streamClosed(stores[1].spans))
		require.False(t, streamClosed(stores[2].spans))
		require.NoError(t, registry.Close())
		require.Equal(t, OpenStats{}, registry.OpenStats())
	})
}
