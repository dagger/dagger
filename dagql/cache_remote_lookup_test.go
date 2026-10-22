package dagql

import (
	"context"
	"fmt"
	"slices"
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

// lazyLookupResult publishes a lazy result through a miss and returns it with
// the miss queued for it, and a channel that receives when its evaluation
// runs.
func lazyLookupResult(t *testing.T, field string) (context.Context, *Cache, *RemoteCacheBridge, ObjectResult[*cacheTestObject], RemoteMiss, <-chan struct{}) {
	t.Helper()
	ctx, c, srv, sessionID, bridge := lookupTestCache(t)
	bridge.StartRemoteLookups()
	ran := make(chan struct{}, 1)
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: field,
	}
	res, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
			Value: 1,
			lazyEval: func(context.Context) error {
				ran <- struct{}{}
				return nil
			},
		}), nil
	})
	require.NoError(t, err)
	misses := queuedMisses(t, bridge)
	require.Len(t, misses, 1)
	return ctx, c, bridge, res.(ObjectResult[*cacheTestObject]), misses[0], ran
}

// An evaluation of a missed call's entry, through either entry point, waits
// for the merge the service said carries its value, and runs once that merge
// ends. The expectation retires with the merge.
func TestRemoteLookupEvaluationWaitsForMerge(t *testing.T) {
	for _, entry := range []string{"Evaluate", "EvaluateParts"} {
		t.Run(entry, func(t *testing.T) {
			ctx, c, bridge, res, miss, ran := lazyLookupResult(t, "awaited")
			merged := NewRemoteMergeWait()
			bridge.ExpectRemoteMerge(miss.Recipe, merged, time.Now().Add(time.Minute))
			evaluated := make(chan error, 1)
			go func() {
				if entry == "Evaluate" {
					evaluated <- c.Evaluate(ctx, res)
				} else {
					evaluated <- c.EvaluateParts(ctx, res)
				}
			}()
			select {
			case <-ran:
				t.Fatal("the evaluation ran before the merge ended")
			case <-time.After(200 * time.Millisecond):
			}
			merged.End(RemoteMergeMerged)
			require.NoError(t, within(t, evaluated))
			within(t, ran)
			require.Eventually(t, func() bool { return bridge.lookups.expecting.Load() == 0 }, 5*time.Second, 10*time.Millisecond, "the expectation retires with its merge")
		})
	}
}

// One evaluation that gives up leaves the expectation to the others: a later
// evaluation of the entry still waits for the merge.
func TestRemoteLookupCanceledWaiterKeepsExpectation(t *testing.T) {
	ctx, c, bridge, res, miss, ran := lazyLookupResult(t, "canceled")
	merged := NewRemoteMergeWait()
	bridge.ExpectRemoteMerge(miss.Recipe, merged, time.Now().Add(time.Minute))
	waitCtx, cancel := context.WithCancel(ctx)
	gaveUp := make(chan struct{})
	go func() {
		c.awaitRemoteMerge(waitCtx, res.cacheSharedResult())
		close(gaveUp)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	within(t, gaveUp)
	require.EqualValues(t, 1, bridge.lookups.expecting.Load())

	evaluated := make(chan error, 1)
	go func() { evaluated <- c.Evaluate(ctx, res) }()
	select {
	case <-ran:
		t.Fatal("the evaluation ran before the merge ended")
	case <-time.After(200 * time.Millisecond):
	}
	merged.End(RemoteMergeMerged)
	require.NoError(t, within(t, evaluated))
	within(t, ran)
}

// An expectation no evaluation waits for retires when its merge ends, or at
// its deadline, and a newer expectation of the same recipe isn't retired by
// an older one's end.
func TestRemoteLookupExpectationsRetire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge, err := newRemoteCacheBridge(new(sync.Mutex))
		require.NoError(t, err)
		merged := NewRemoteMergeWait()
		bridge.ExpectRemoteMerge("xxh3:merged", merged, time.Now().Add(time.Minute))
		bridge.ExpectRemoteMerge("xxh3:late", NewRemoteMergeWait(), time.Now().Add(time.Second))
		require.EqualValues(t, 2, bridge.lookups.expecting.Load())
		merged.End(RemoteMergeMerged)
		synctest.Wait()
		require.EqualValues(t, 1, bridge.lookups.expecting.Load(), "the merge's end retires its expectation")
		time.Sleep(time.Second)
		synctest.Wait()
		require.Zero(t, bridge.lookups.expecting.Load(), "the deadline retires the other")

		older, newer := NewRemoteMergeWait(), NewRemoteMergeWait()
		bridge.ExpectRemoteMerge("xxh3:again", older, time.Now().Add(time.Minute))
		bridge.ExpectRemoteMerge("xxh3:again", newer, time.Now().Add(time.Minute))
		older.End(RemoteMergeMerged)
		synctest.Wait()
		require.EqualValues(t, 1, bridge.lookups.expecting.Load())
		newer.End(RemoteMergeMerged)
		synctest.Wait()
		require.Zero(t, bridge.lookups.expecting.Load())

		// Detaching retires every expectation's goroutine.
		bridge.ExpectRemoteMerge("xxh3:detached", NewRemoteMergeWait(), time.Now().Add(time.Minute))
		bridge.mu.Lock()
		bridge.closeLocked()
		bridge.mu.Unlock()
		synctest.Wait()
		require.Zero(t, bridge.lookups.expecting.Load())
	})
}

// An offer that arrives while an evaluation waits for its merge is installed
// once the wait ends, and the entry's operation never runs.
func TestRemoteLookupWaitThenInstalls(t *testing.T) {
	chain := newChainFixture(t, "lookup-wait")
	f := newExhaustionFixture(t, chain)
	bridge, _, err := f.cache.AttachRemoteCacheBridge()
	require.NoError(t, err)
	f.cache.egraphMu.RLock()
	recipes := slices.Clone(f.receiver.cacheSharedResult().recipeKeys)
	f.cache.egraphMu.RUnlock()
	require.NotEmpty(t, recipes)
	merged := NewRemoteMergeWait()
	bridge.ExpectRemoteMerge(recipes[0], merged, time.Now().Add(time.Minute))
	waited := make(chan struct{})
	go func() {
		f.cache.awaitRemoteMerge(f.ctx, f.receiver.cacheSharedResult())
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("the wait ended before the merge")
	case <-time.After(100 * time.Millisecond):
	}
	f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
	merged.End(RemoteMergeMerged)
	within(t, waited)
	require.NoError(t, f.run())
	require.True(t, f.installed(), "the part installed from the merged offer")
	require.Zero(t, f.operation.runs.Load(), "the operation never ran")
}

// The wait ends at its deadline when the merge doesn't.
func TestRemoteLookupEvaluationWaitIsBounded(t *testing.T) {
	ctx, c, bridge, res, miss, ran := lazyLookupResult(t, "bounded")
	bridge.ExpectRemoteMerge(miss.Recipe, NewRemoteMergeWait(), time.Now().Add(300*time.Millisecond))
	start := time.Now()
	require.NoError(t, c.Evaluate(ctx, res))
	within(t, ran)
	waited := time.Since(start)
	require.GreaterOrEqual(t, waited, 250*time.Millisecond)
	require.Less(t, waited, 5*time.Second)
}

// Evaluations of other entries, and of an entry whose expectation has passed
// its deadline, don't wait.
func TestRemoteLookupEvaluationWithoutExpectation(t *testing.T) {
	ctx, c, bridge, res, miss, ran := lazyLookupResult(t, "unawaited")
	bridge.ExpectRemoteMerge("xxh3:other", NewRemoteMergeWait(), time.Now().Add(time.Minute))
	bridge.ExpectRemoteMerge(miss.Recipe, NewRemoteMergeWait(), time.Now().Add(-time.Second))
	start := time.Now()
	require.NoError(t, c.Evaluate(ctx, res))
	within(t, ran)
	require.Less(t, time.Since(start), 250*time.Millisecond)
}

// Detaching the bridge releases an evaluation that is waiting, and forgets
// the expectations.
func TestRemoteLookupDetachReleasesWaits(t *testing.T) {
	ctx, c, bridge, res, miss, ran := lazyLookupResult(t, "detached")
	bridge.ExpectRemoteMerge(miss.Recipe, NewRemoteMergeWait(), time.Now().Add(time.Minute))
	evaluated := make(chan error, 1)
	go func() { evaluated <- c.Evaluate(ctx, res) }()
	select {
	case <-ran:
		t.Fatal("the evaluation ran before the detach")
	case <-time.After(200 * time.Millisecond):
	}
	require.True(t, c.DetachRemoteCacheBridge(bridge))
	require.NoError(t, within(t, evaluated))
	within(t, ran)
	require.Zero(t, bridge.lookups.expecting.Load())
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

// The lazy-evaluation span's state reports the entry's last wait for a
// remote lookup's merge and how it ended, and no wait when none ran.
func TestRemoteLookupWaitOnLazySpanState(t *testing.T) {
	ctx, c, bridge, res, miss, ran := lazyLookupResult(t, "span-state")
	require.Nil(t, c.lazySpanCacheState(ctx, res.cacheSharedResult(), nil).lookupWait)
	// An expectation whose merge has ended, before it retires.
	ended := NewRemoteMergeWait()
	ended.End(RemoteMergeDisconnected)
	exp := remoteMergeExpectation{wait: ended, deadline: time.Now().Add(time.Minute)}
	bridge.lookups.mu.Lock()
	bridge.lookups.expected[miss.Recipe] = exp
	bridge.lookups.expecting.Store(1)
	bridge.lookups.mu.Unlock()
	c.awaitRemoteMerge(ctx, res.cacheSharedResult())
	bridge.lookups.forget(miss.Recipe, exp)
	wait := c.lazySpanCacheState(ctx, res.cacheSharedResult(), nil).lookupWait
	require.NotNil(t, wait)
	require.Equal(t, RemoteMergeDisconnected, wait.outcome, "the wait reports why its merge ended")

	bridge.ExpectRemoteMerge(miss.Recipe, NewRemoteMergeWait(), time.Now().Add(time.Second))
	require.NoError(t, c.Evaluate(ctx, res))
	within(t, ran)
	wait = c.lazySpanCacheState(ctx, res.cacheSharedResult(), nil).lookupWait
	require.NotNil(t, wait)
	require.Equal(t, RemoteMergeDeadline, wait.outcome)
	require.Positive(t, wait.waited)
}
