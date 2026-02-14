package dagql

import (
	"context"
	"fmt"
)

func ArbitraryValueFunc(v any) func(context.Context) (any, error) {
	return func(context.Context) (any, error) {
		return v, nil
	}
}

type ArbitraryCachedResult interface {
	Value() any
	HitCache() bool
	Release(context.Context) error
}

// sharedArbitraryResult is the in-memory-only cache entry for GetOrInitArbitrary values.
type sharedArbitraryResult struct {
	cache *cache

	callKey string

	value any
	err   error

	onRelease OnReleaseFunc

	waitCh  chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	refCount int
}

func (res *sharedArbitraryResult) release(ctx context.Context) error {
	if res == nil || res.cache == nil {
		return nil
	}

	res.cache.mu.Lock()
	res.refCount--
	var onRelease OnReleaseFunc
	if res.refCount == 0 && res.waiters == 0 {
		delete(res.cache.ongoingArbitraryCalls, res.callKey)
		if existing := res.cache.completedArbitraryCalls[res.callKey]; existing == res {
			delete(res.cache.completedArbitraryCalls, res.callKey)
		}
		onRelease = res.onRelease
	}
	res.cache.mu.Unlock()

	if onRelease != nil {
		return onRelease(ctx)
	}
	return nil
}

type arbitraryResult struct {
	shared   *sharedArbitraryResult
	hitCache bool
}

var _ ArbitraryCachedResult = arbitraryResult{}

func (r arbitraryResult) Value() any {
	if r.shared == nil {
		return nil
	}
	return r.shared.value
}

func (r arbitraryResult) HitCache() bool {
	return r.hitCache
}

func (r arbitraryResult) Release(ctx context.Context) error {
	if r.shared == nil {
		return nil
	}
	return r.shared.release(ctx)
}

type arbitraryCacheContextKey struct {
	callKey string
	cache   *cache
}

func (c *cache) GetOrInitArbitrary(
	ctx context.Context,
	callKey string,
	fn func(context.Context) (any, error),
) (ArbitraryCachedResult, error) {
	if callKey == "" {
		return nil, fmt.Errorf("cache call key is empty")
	}

	if ctx.Value(arbitraryCacheContextKey{callKey: callKey, cache: c}) != nil {
		return nil, ErrCacheRecursiveCall
	}

	c.mu.Lock()
	if c.ongoingArbitraryCalls == nil {
		c.ongoingArbitraryCalls = make(map[string]*sharedArbitraryResult)
	}
	if c.completedArbitraryCalls == nil {
		c.completedArbitraryCalls = make(map[string]*sharedArbitraryResult)
	}

	if res := c.completedArbitraryCalls[callKey]; res != nil {
		res.refCount++
		c.mu.Unlock()
		return arbitraryResult{
			shared:   res,
			hitCache: true,
		}, nil
	}

	if res := c.ongoingArbitraryCalls[callKey]; res != nil {
		res.waiters++
		c.mu.Unlock()
		return c.waitArbitrary(ctx, res, false)
	}

	callCtx := context.WithValue(ctx, arbitraryCacheContextKey{callKey: callKey, cache: c}, struct{}{})
	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	res := &sharedArbitraryResult{
		cache: c,

		callKey: callKey,

		waitCh:  make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}
	c.ongoingArbitraryCalls[callKey] = res

	go func() {
		defer close(res.waitCh)
		val, err := fn(callCtx)
		res.err = err
		if err == nil {
			res.value = val
			if onReleaser, ok := val.(OnReleaser); ok {
				res.onRelease = onReleaser.OnRelease
			}
		}
	}()

	c.mu.Unlock()
	return c.waitArbitrary(ctx, res, true)
}

func (c *cache) waitArbitrary(ctx context.Context, res *sharedArbitraryResult, isFirstCaller bool) (ArbitraryCachedResult, error) {
	var hitCache bool
	var err error

	select {
	case <-res.waitCh:
		hitCache = true
		err = res.err
	default:
		select {
		case <-res.waitCh:
			err = res.err
		case <-ctx.Done():
			err = context.Cause(ctx)
		}
	}

	c.mu.Lock()
	res.waiters--
	if res.waiters == 0 {
		res.cancel(err)
	}

	if err == nil {
		delete(c.ongoingArbitraryCalls, res.callKey)
		if existing := c.completedArbitraryCalls[res.callKey]; existing != nil {
			res = existing
		} else {
			c.completedArbitraryCalls[res.callKey] = res
		}
		res.refCount++
		c.mu.Unlock()

		if isFirstCaller {
			hitCache = false
		}
		return arbitraryResult{
			shared:   res,
			hitCache: hitCache,
		}, nil
	}

	if res.refCount == 0 && res.waiters == 0 {
		if existing := c.ongoingArbitraryCalls[res.callKey]; existing == res {
			delete(c.ongoingArbitraryCalls, res.callKey)
		}
		if existing := c.completedArbitraryCalls[res.callKey]; existing == res {
			delete(c.completedArbitraryCalls, res.callKey)
		}
	}

	c.mu.Unlock()
	return nil, err
}
