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
// digests, its term and its field, once per connection, and only while a
// connection serves lookups. A hit, and a call that isn't cached, queue
// nothing.
func TestRemoteLookupQueuesMisses(t *testing.T) {
	ctx, c, srv, sessionID, bridge := lookupTestCache(t)
	extra := call.ExtraDigest{Digest: digest.FromString("content"), Label: "content"}
	frame := cacheTestIntCall("missed", extra)
	run := func(frame *ResultCall, doNotCache bool) {
		t.Helper()
		_, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame.clone(), DoNotCache: doNotCache, CacheReported: true}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(frame, 1), nil
		})
		require.NoError(t, err)
	}

	run(cacheTestIntCall("before"), false)
	require.Empty(t, queuedMisses(t, bridge), "nothing is queued while no connection serves lookups")

	bridge.StartRemoteLookups()
	before := time.Now()
	req := &CallRequest{ResultCall: frame.clone(), CacheEvidence: NewCacheDecision(), CacheReported: true}
	_, err := c.GetOrInitCall(ctx, sessionID, srv, req, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1), nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, req.CacheEvidence.SelfDigest)
	missed := queuedMisses(t, bridge)
	require.Len(t, missed, 1)
	require.False(t, missed[0].At.Before(before), "the miss is stamped with when it missed")
	require.False(t, missed[0].At.After(time.Now()))
	missed[0].At = time.Time{}
	require.Equal(t, []RemoteMiss{{
		Recipe: cacheTestCallDigest(frame),
		Extra:  []digest.Digest{extra.Digest},
		Field:  "missed",
		Self:   req.CacheEvidence.SelfDigest,
		Inputs: req.CacheEvidence.StructuralInputs,
	}}, missed, "the miss names its recipe, extras and the term its lookup tried")

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

// The service learns a call only from the cache decision telemetry reports
// for it, so a miss asks it only about a call whose decision is reported: a
// call telemetry leaves out, such as one building a module's type
// definitions, queues nothing, with or without evidence armed for profiling.
func TestRemoteLookupOnlyReportedCalls(t *testing.T) {
	ctx, c, srv, sessionID, bridge := lookupTestCache(t)
	bridge.StartRemoteLookups()
	miss := func(field string, reported bool, evidence *CacheDecision) *CallRequest {
		t.Helper()
		frame := cacheTestIntCall(field)
		req := &CallRequest{ResultCall: frame.clone(), CacheReported: reported, CacheEvidence: evidence}
		_, err := c.GetOrInitCall(ctx, sessionID, srv, req, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(frame, 1), nil
		})
		require.NoError(t, err)
		return req
	}

	miss("unreported", false, nil)
	armed := miss("armed", false, NewCacheDecision())
	require.Empty(t, queuedMisses(t, bridge), "a call telemetry doesn't report asks the service nothing")
	require.Empty(t, armed.CacheEvidence.MissRemoteLookup, "its evidence records no lookup")

	reported := miss("reported", true, NewCacheDecision())
	misses := queuedMisses(t, bridge)
	require.Len(t, misses, 1, "a reported call's miss is asked")
	require.Equal(t, cacheTestCallDigest(cacheTestIntCall("reported")), misses[0].Recipe)
	require.Equal(t, RemoteLookupQueued, reported.CacheEvidence.MissRemoteLookup)

	miss("reported-unarmed", true, nil)
	require.Len(t, queuedMisses(t, bridge), 1, "whether this engine's span records doesn't matter: another engine's may have")
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
	_, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame.clone(), CacheReported: true}, func(context.Context) (AnyResult, error) {
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

// A miss queued before a merge is late for it when its lookup now finds an
// entry the merge landed on: it is returned once, never for a merge that
// landed elsewhere or for a miss after the merge, and not on a later
// connection.
func TestLateRemoteMisses(t *testing.T) {
	ctx, c, srv, sessionID, bridge := lookupTestCache(t)
	bridge.StartRemoteLookups()
	frame := cacheTestIntCall("late")
	beforeMiss := time.Now()
	res, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame.clone(), CacheReported: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1), nil
	})
	require.NoError(t, err)
	landed := map[uint64]struct{}{uint64(res.cacheSharedResult().id): {}}

	require.Empty(t, c.LateRemoteMisses(beforeMiss, landed), "a miss after the merge isn't late for it")
	require.Empty(t, c.LateRemoteMisses(time.Now(), map[uint64]struct{}{uint64(res.cacheSharedResult().id) + 1000: {}}), "a merge that landed elsewhere wouldn't have served it")
	late := c.LateRemoteMisses(time.Now(), landed)
	require.Len(t, late, 1)
	require.Equal(t, cacheTestCallDigest(frame), late[0].Recipe)
	require.Empty(t, c.LateRemoteMisses(time.Now(), landed), "a miss is late once")

	other := cacheTestIntCall("late on the next connection")
	res, err = c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: other.clone(), CacheReported: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(other, 2), nil
	})
	require.NoError(t, err)
	bridge.StopRemoteLookups()
	bridge.StartRemoteLookups()
	require.Empty(t, c.LateRemoteMisses(time.Now(), map[uint64]struct{}{uint64(res.cacheSharedResult().id): {}}), "a connection's misses end with it")
}

// A full miss log makes room by dropping the misses already returned, then
// its oldest, so a merge that lands late on a long-lived connection is still
// counted against the recent misses.
func TestLateRemoteMissesKeepTheRecentMisses(t *testing.T) {
	_, c, _, _, bridge := lookupTestCache(t) //nolint:dogsled // the test reads only the cache and its bridge
	bridge.StartRemoteLookups()
	l := &bridge.lookups
	miss := func(i int) RemoteMiss {
		return RemoteMiss{Recipe: digest.FromString(fmt.Sprint("recipe", i)), At: time.Now()}
	}
	for i := range remoteLookupLogCapacity {
		bridge.noteRemoteMiss(miss(i))
		// Keep the queue from refusing misses: only the log is under test.
		l.mu.Lock()
		l.queue = l.queue[:0]
		l.mu.Unlock()
	}
	bridge.noteRemoteMiss(miss(remoteLookupLogCapacity))
	require.Equal(t, uint64(remoteLookupLogCapacity/4), c.TrimmedRemoteMisses(), "the misses dropped uncounted are counted")
	l.mu.Lock()
	defer l.mu.Unlock()
	require.Len(t, l.missed, remoteLookupLogCapacity*3/4+1)
	require.Equal(t, miss(remoteLookupLogCapacity/4).Recipe, l.missed[0].miss.Recipe, "the oldest quarter is dropped")
	require.Equal(t, miss(remoteLookupLogCapacity).Recipe, l.missed[len(l.missed)-1].miss.Recipe, "the newest miss is kept")
}

// A trim of the log while a scan checks its candidates doesn't lose the
// scan: a late miss the trim kept is still returned, once.
func TestLateRemoteMissesSurviveATrim(t *testing.T) {
	ctx, c, srv, sessionID, bridge := lookupTestCache(t)
	bridge.StartRemoteLookups()
	frame := cacheTestIntCall("late across a trim")
	res, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame.clone(), CacheReported: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(frame, 1), nil
	})
	require.NoError(t, err)
	l := &bridge.lookups
	// The trim lands while the scan is between its two holds of the lock:
	// an older miss is added first, so the trim drops something.
	l.mu.Lock()
	l.missed = append([]loggedMiss{{miss: RemoteMiss{Recipe: "xxh3:old", At: time.Now()}, found: true}}, l.missed...)
	l.mu.Unlock()
	landed := map[uint64]struct{}{uint64(res.cacheSharedResult().id): {}}
	c.testBetweenLateMissHolds = func() {
		l.mu.Lock()
		l.trimMissedLocked()
		l.mu.Unlock()
	}
	late := c.LateRemoteMisses(time.Now(), landed)
	require.Len(t, late, 1)
	require.Equal(t, cacheTestCallDigest(frame), late[0].Recipe)
	c.testBetweenLateMissHolds = nil
	require.Empty(t, c.LateRemoteMisses(time.Now(), landed))
}
