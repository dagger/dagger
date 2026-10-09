package dagql

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// runWhileEgraphReadLocked runs f while another goroutine holds egraphMu for
// reading, and fails if f does not finish: a path that takes the write lock
// waits for the reader and never does.
func runWhileEgraphReadLocked(t *testing.T, c *Cache, f func() error) {
	t.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		assert.NilError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("waited for the egraph write lock behind a reader")
	}
}

// The per-call paths that only read the e-graph share egraphMu: a cache hit
// that teaches nothing, a miss, session result tracking and a session's ID
// load. Under hundreds of concurrent calls, write-locking them queued every
// call behind every other.
func TestCacheReadPathsShareEgraphLock(t *testing.T) {
	ctx := t.Context()
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	call := cacheTestIntCall("shared-lock")
	seeded, err := c.GetOrInitCall(ctx, "seed", noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(call, 1), nil
	})
	assert.NilError(t, err)
	seededID := seeded.cacheSharedResult().id

	t.Run("hit", func(t *testing.T) {
		runWhileEgraphReadLocked(t, c, func() error {
			res, err := c.GetOrInitCall(ctx, "hit", noopTypeResolver{}, &CallRequest{ResultCall: cacheTestIntCall("shared-lock")}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("hit executed the call")
			})
			if err != nil {
				return err
			}
			if !res.HitCache() || res.cacheSharedResult().id != seededID {
				return fmt.Errorf("expected a hit on result %d, got hit=%v result %d", seededID, res.HitCache(), res.cacheSharedResult().id)
			}
			return nil
		})
	})

	t.Run("miss", func(t *testing.T) {
		runWhileEgraphReadLocked(t, c, func() error {
			_, hit, err := c.lookupCallRequest(ctx, "miss", noopTypeResolver{}, &CallRequest{ResultCall: cacheTestIntCall("shared-lock-unknown")})
			if err != nil {
				return err
			}
			if hit {
				return fmt.Errorf("unknown call hit")
			}
			return nil
		})
	})

	t.Run("track", func(t *testing.T) {
		runWhileEgraphReadLocked(t, c, func() error {
			return c.trackSessionResult(ctx, "track", seeded, true)
		})
	})

	t.Run("id load", func(t *testing.T) {
		runWhileEgraphReadLocked(t, c, func() error {
			lookup, err := c.sharedResultByResultID(ctx, "load", seededID, sharedResultLookupCanonicalEquivalentForSession)
			if err != nil {
				return err
			}
			if lookup.res.id != seededID {
				return fmt.Errorf("expected result %d, got %d", seededID, lookup.res.id)
			}
			return nil
		})
	})

	// A persistable hit teaches the e-graph and records a retention edge,
	// so it still takes the write lock, and still hits.
	res, err := c.GetOrInitCall(ctx, "persistable", noopTypeResolver{}, &CallRequest{ResultCall: cacheTestIntCall("shared-lock"), IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("persistable hit executed the call")
	})
	assert.NilError(t, err)
	assert.Assert(t, res.HitCache())
	assert.Equal(t, res.cacheSharedResult().id, seededID)
	c.egraphMu.RLock()
	_, retained := c.persistedEdgesByResult[seededID]
	c.egraphMu.RUnlock()
	assert.Assert(t, retained)

	assertCacheOwnershipExact(t, c)
	for _, sessionID := range []string{"seed", "hit", "miss", "track", "load", "persistable"} {
		assert.NilError(t, c.ReleaseSession(ctx, sessionID))
	}
	assertCacheOwnershipExact(t, c)
}

// Concurrent sessions hitting, tracking and loading the same results under
// the read lock, interleaved with releases under the write lock, keep the
// ownership counts exact.
func TestCacheConcurrentSharedClaimsKeepOwnershipExact(t *testing.T) {
	ctx := t.Context()
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	const results = 8
	seeded := make([]AnyResult, results)
	for i := range seeded {
		call := cacheTestIntCall(fmt.Sprintf("shared-claims-%d", i))
		seeded[i], err = c.GetOrInitCall(ctx, "seed", noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(call, i), nil
		})
		assert.NilError(t, err)
	}

	const sessions = 32
	var wg sync.WaitGroup
	errs := make(chan error, sessions)
	for s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sessionID := fmt.Sprintf("claimer-%d", s)
			for round := range 4 {
				for i := range results {
					switch (s + round + i) % 3 {
					case 0:
						res, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: cacheTestIntCall(fmt.Sprintf("shared-claims-%d", i))}, func(context.Context) (AnyResult, error) {
							return nil, fmt.Errorf("hit executed the call")
						})
						if err != nil {
							errs <- err
							return
						}
						if !res.HitCache() {
							errs <- fmt.Errorf("expected a hit")
							return
						}
					case 1:
						if err := c.trackSessionResult(ctx, sessionID, seeded[i], true); err != nil {
							errs <- err
							return
						}
					case 2:
						if _, err := c.sharedResultByResultID(ctx, sessionID, seeded[i].cacheSharedResult().id, sharedResultLookupCanonicalEquivalentForSession); err != nil {
							errs <- err
							return
						}
					}
				}
			}
			if s%2 == 0 {
				if err := c.ReleaseSession(ctx, sessionID); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		assert.NilError(t, err)
	}

	assertCacheOwnershipExact(t, c)
	for s := range sessions {
		if s%2 != 0 {
			assert.NilError(t, c.ReleaseSession(ctx, fmt.Sprintf("claimer-%d", s)))
		}
	}
	assert.NilError(t, c.ReleaseSession(ctx, "seed"))
	assertCacheOwnershipExact(t, c)
	c.egraphMu.RLock()
	remaining := len(c.resultsByID)
	c.egraphMu.RUnlock()
	assert.Equal(t, remaining, 0)
}

// BenchmarkCacheParallelHits measures concurrent cache hits from many
// sessions, the shape of a cached replay's call storm.
func BenchmarkCacheParallelHits(b *testing.B) {
	ctx := b.Context()
	c, err := NewCache(ctx, "", nil, nil)
	assert.NilError(b, err)
	const results = 64
	for i := range results {
		call := cacheTestIntCall(fmt.Sprintf("parallel-hits-%d", i))
		_, err := c.GetOrInitCall(ctx, "seed", noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(call, i), nil
		})
		assert.NilError(b, err)
	}
	calls := make([]*ResultCall, results)
	for i := range calls {
		calls[i] = cacheTestIntCall(fmt.Sprintf("parallel-hits-%d", i))
	}
	var next sync.Mutex
	session := 0
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		next.Lock()
		sessionID := fmt.Sprintf("bench-%d", session)
		session++
		next.Unlock()
		i := 0
		for pb.Next() {
			res, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: calls[i%results]}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("hit executed the call")
			})
			if err != nil || !res.HitCache() {
				b.Errorf("expected a hit: %v", err)
				return
			}
			i++
		}
	})
}
