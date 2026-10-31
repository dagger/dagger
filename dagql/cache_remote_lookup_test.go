package dagql

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func lookupTestCache(t *testing.T) (context.Context, *Cache, *Server, string, *RemoteCacheBridge) {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	bridge, created, err := c.AttachRemoteCacheBridge()
	require.NoError(t, err)
	require.True(t, created)
	return ctx, c, cacheTestServer(t), cacheTestSessionID(t, ctx), bridge
}

// queuedMisses returns the misses queued now, without waiting for more.
func queuedMisses(t *testing.T, bridge *RemoteCacheBridge) []RemoteMiss {
	t.Helper()
	bridge.lookups.mu.Lock()
	n := len(bridge.lookups.queue)
	bridge.lookups.mu.Unlock()
	if n == 0 {
		return nil
	}
	misses, _, err := bridge.TakeRemoteMisses(t.Context(), n, 0)
	require.NoError(t, err)
	return misses
}

// A call the cache misses is queued for the service by its recipe, its extra
// digests and its field, once per connection, and only while a connection
// serves lookups. A hit, and a call that isn't cached, queue nothing.
func TestRemoteLookupQueuesMisses(t *testing.T) {
	ctx, c, srv, sessionID, bridge := lookupTestCache(t)
	extra := call.ExtraDigest{Digest: digest.FromString("content"), Label: "content"}
	frame := cacheTestIntCall("missed", extra)
	run := func(frame *ResultCall, doNotCache bool) {
		t.Helper()
		_, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame.clone(), DoNotCache: doNotCache}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(frame, 1), nil
		})
		require.NoError(t, err)
	}

	run(cacheTestIntCall("before"), false)
	require.Empty(t, queuedMisses(t, bridge), "nothing is queued while no connection serves lookups")

	bridge.StartRemoteLookups()
	run(frame, false)
	require.Equal(t, []RemoteMiss{{Recipe: cacheTestCallDigest(frame), Extra: []digest.Digest{extra.Digest}, Field: "missed"}}, queuedMisses(t, bridge))

	run(frame, false)
	require.Empty(t, queuedMisses(t, bridge), "a hit queues nothing")
	run(cacheTestIntCall("uncached"), true)
	require.Empty(t, queuedMisses(t, bridge), "a call that isn't cached queues nothing")

	// The same recipe missed again on the connection isn't asked for twice;
	// a new connection asks afresh.
	bridge.noteRemoteMiss(RemoteMiss{Recipe: cacheTestCallDigest(frame)})
	require.Empty(t, queuedMisses(t, bridge))
	bridge.StopRemoteLookups()
	run(cacheTestIntCall("between"), false)
	require.Empty(t, queuedMisses(t, bridge), "nothing is queued between connections")
	bridge.StartRemoteLookups()
	bridge.noteRemoteMiss(RemoteMiss{Recipe: cacheTestCallDigest(frame)})
	require.Len(t, queuedMisses(t, bridge), 1)
}

// A miss names at most remoteLookupMaxExtras extra digests.
func TestRemoteLookupBoundsExtras(t *testing.T) {
	ctx, c, srv, sessionID, bridge := lookupTestCache(t)
	bridge.StartRemoteLookups()
	var extras []call.ExtraDigest
	for i := range remoteLookupMaxExtras + 2 {
		extras = append(extras, call.ExtraDigest{Digest: digest.FromString(fmt.Sprint("extra", i)), Label: "extra"})
	}
	frame := cacheTestIntCall("extras", extras...)
	_, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame.clone()}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1), nil
	})
	require.NoError(t, err)
	misses := queuedMisses(t, bridge)
	require.Len(t, misses, 1)
	require.Len(t, misses[0].Extra, remoteLookupMaxExtras)
	require.Equal(t, extras[0].Digest, misses[0].Extra[0])
}

// The queue holds remoteLookupQueueCapacity misses; past it a miss is
// dropped and counted, and a dropped recipe can be asked for later.
func TestRemoteLookupQueueBound(t *testing.T) {
	bridge, err := newRemoteCacheBridge(new(sync.Mutex))
	require.NoError(t, err)
	bridge.StartRemoteLookups()
	for i := range remoteLookupQueueCapacity + 2 {
		bridge.noteRemoteMiss(RemoteMiss{Recipe: digest.FromString(fmt.Sprint("recipe", i))})
	}
	misses, dropped, err := bridge.TakeRemoteMisses(t.Context(), 32, 0)
	require.NoError(t, err)
	require.Len(t, misses, 32)
	require.Equal(t, digest.FromString("recipe0"), misses[0].Recipe, "misses are taken in order")
	require.Equal(t, uint64(2), dropped)
	_, dropped, err = bridge.TakeRemoteMisses(t.Context(), 32, 0)
	require.NoError(t, err)
	require.Zero(t, dropped, "a take reports the drops since the last one")

	bridge.noteRemoteMiss(RemoteMiss{Recipe: digest.FromString(fmt.Sprint("recipe", remoteLookupQueueCapacity))})
	bridge.lookups.mu.Lock()
	queued := len(bridge.lookups.queue)
	bridge.lookups.mu.Unlock()
	require.Equal(t, remoteLookupQueueCapacity-64+1, queued, "a dropped recipe is queued when asked again")
}

// A take waits for a miss, then lingers for more until it has its limit or
// the linger ends.
func TestRemoteLookupTakeLingers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge, err := newRemoteCacheBridge(new(sync.Mutex))
		require.NoError(t, err)
		bridge.StartRemoteLookups()
		type taken struct {
			misses []RemoteMiss
			at     time.Time
		}
		take := func(limit int) <-chan taken {
			out := make(chan taken, 1)
			go func() {
				misses, _, err := bridge.TakeRemoteMisses(t.Context(), limit, 25*time.Millisecond)
				if err != nil {
					panic(err)
				}
				out <- taken{misses, time.Now()}
			}()
			return out
		}

		got := take(3)
		time.Sleep(time.Second)
		synctest.Wait()
		start := time.Now()
		bridge.noteRemoteMiss(RemoteMiss{Recipe: "xxh3:1"})
		result := <-got
		require.Len(t, result.misses, 1)
		require.Equal(t, 25*time.Millisecond, result.at.Sub(start), "one miss waits out the linger")

		got = take(3)
		synctest.Wait()
		start = time.Now()
		for i := range 3 {
			bridge.noteRemoteMiss(RemoteMiss{Recipe: digest.Digest(fmt.Sprint("xxh3:", 10+i))})
		}
		result = <-got
		require.Len(t, result.misses, 3)
		require.Equal(t, start, result.at, "a full batch goes at once")

		// Detaching wakes the taker.
		got2 := make(chan error, 1)
		go func() {
			_, _, err := bridge.TakeRemoteMisses(t.Context(), 3, 25*time.Millisecond)
			got2 <- err
		}()
		synctest.Wait()
		bridge.mu.Lock()
		bridge.closeLocked()
		bridge.mu.Unlock()
		require.ErrorIs(t, <-got2, ErrRemoteCacheBridgeClosed)
		bridge.noteRemoteMiss(RemoteMiss{Recipe: "xxh3:99"})
		_, _, err = bridge.TakeRemoteMisses(t.Context(), 3, 0)
		require.ErrorIs(t, err, ErrRemoteCacheBridgeClosed)
	})
}

// A miss records, on its evidence, what it did about a remote lookup: queued
// one, found its recipe already asked for, or found the queue full. With no
// connection serving lookups it records nothing.
func TestRemoteLookupMissFact(t *testing.T) {
	ctx, c, srv, sessionID, bridge := lookupTestCache(t)
	miss := func(field string) string {
		t.Helper()
		frame := cacheTestIntCall(field)
		req := cacheTestArmedRequest(frame)
		_, err := c.GetOrInitCall(ctx, sessionID, srv, req, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(frame, 1), nil
		})
		require.NoError(t, err)
		return req.CacheEvidence.MissRemoteLookup
	}
	require.Empty(t, miss("off"))
	bridge.StartRemoteLookups()
	require.Equal(t, RemoteLookupQueued, miss("queued"))
	require.Equal(t, RemoteLookupAsked, bridge.noteRemoteMiss(RemoteMiss{Recipe: cacheTestCallDigest(cacheTestIntCall("queued"))}))
	for i := range remoteLookupQueueCapacity {
		bridge.noteRemoteMiss(RemoteMiss{Recipe: digest.FromString(fmt.Sprint("filler", i))})
	}
	require.Equal(t, RemoteLookupDropped, miss("dropped"))
}
