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
}

// sharedArbitraryResult is the in-memory-only cache entry for GetOrInitArbitrary values.
type sharedArbitraryResult struct {
	callKey string

	value any
	err   error

	onRelease OnReleaseFunc

	waitCh  chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	ownerSessionCount int
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

type arbitraryCacheContextKey struct {
	callKey string
}

func (c *Cache) GetOrInitArbitrary(
	ctx context.Context,
	sessionID string,
	callKey string,
	fn func(context.Context) (any, error),
) (ArbitraryCachedResult, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("get or init arbitrary %q: empty session ID", callKey)
	}
	if callKey == "" {
		return nil, fmt.Errorf("cache call key is empty")
	}

	if ctx.Value(arbitraryCacheContextKey{callKey: callKey}) != nil {
		return nil, ErrCacheRecursiveCall
	}

	c.callsMu.Lock()
	if c.ongoingArbitraryCalls == nil {
		c.ongoingArbitraryCalls = make(map[string]*sharedArbitraryResult)
	}
	if c.completedArbitraryCalls == nil {
		c.completedArbitraryCalls = make(map[string]*sharedArbitraryResult)
	}

	if res := c.completedArbitraryCalls[callKey]; res != nil {
		c.callsMu.Unlock()
		ret := arbitraryResult{
			shared:   res,
			hitCache: true,
		}
		c.trackSessionArbitrary(sessionID, ret)
		return ret, nil
	}

	if res := c.ongoingArbitraryCalls[callKey]; res != nil {
		res.waiters++
		c.callsMu.Unlock()
		return c.waitArbitrary(ctx, sessionID, res, false)
	}

	callCtx := context.WithValue(ctx, arbitraryCacheContextKey{callKey: callKey}, struct{}{})
	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	res := &sharedArbitraryResult{
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

	c.callsMu.Unlock()
	return c.waitArbitrary(ctx, sessionID, res, true)
}

func (c *Cache) waitArbitrary(ctx context.Context, sessionID string, res *sharedArbitraryResult, isFirstCaller bool) (ArbitraryCachedResult, error) {
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

	c.callsMu.Lock()
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
		c.callsMu.Unlock()

		if isFirstCaller {
			hitCache = false
		}
		ret := arbitraryResult{
			shared:   res,
			hitCache: hitCache,
		}
		c.trackSessionArbitrary(sessionID, ret)
		return ret, nil
	}

	if res.ownerSessionCount == 0 && res.waiters == 0 {
		if existing := c.ongoingArbitraryCalls[res.callKey]; existing == res {
			delete(c.ongoingArbitraryCalls, res.callKey)
		}
		if existing := c.completedArbitraryCalls[res.callKey]; existing == res {
			delete(c.completedArbitraryCalls, res.callKey)
		}
	}

	c.callsMu.Unlock()
	return nil, err
}
