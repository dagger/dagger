package dagql

import (
	"context"
	"errors"
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
	// id is the engine-lifetime-unique identity of this entry. A call key can
	// be legitimately reused after its entry is removed, so removal paths
	// compare ids to confirm a map entry is the one they hold.
	id      uint64
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
		ret := arbitraryResult{
			shared:   res,
			hitCache: true,
		}
		err := c.acquireSessionArbitraryLocked(sessionID, res)
		var onRelease OnReleaseFunc
		if err != nil {
			onRelease = c.removeUnownedArbitraryLocked(res)
		}
		c.callsMu.Unlock()
		if err != nil {
			return nil, errors.Join(err, runArbitraryOnRelease(ctx, onRelease))
		}
		return ret, nil
	}

	if res := c.ongoingArbitraryCalls[callKey]; res != nil {
		res.waiters++
		c.callsMu.Unlock()
		return c.waitArbitrary(ctx, sessionID, res, false)
	}

	callCtx := context.WithValue(ctx, arbitraryCacheContextKey{callKey: callKey}, struct{}{})
	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	c.nextArbitraryResultID++
	res := &sharedArbitraryResult{
		id:      c.nextArbitraryResultID,
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

		if isFirstCaller {
			hitCache = false
		}
		ret := arbitraryResult{
			shared:   res,
			hitCache: hitCache,
		}
		claimErr := c.acquireSessionArbitraryLocked(sessionID, res)
		var onRelease OnReleaseFunc
		if claimErr != nil {
			onRelease = c.removeUnownedArbitraryLocked(res)
		}
		c.callsMu.Unlock()
		if claimErr != nil {
			return nil, errors.Join(claimErr, runArbitraryOnRelease(ctx, onRelease))
		}
		return ret, nil
	}

	c.removeUnownedArbitraryLocked(res)

	c.callsMu.Unlock()
	return nil, err
}

// removeUnownedArbitraryLocked drops an arbitrary value that has neither a
// session owner nor an active waiter. The caller must hold callsMu.
func (c *Cache) removeUnownedArbitraryLocked(res *sharedArbitraryResult) OnReleaseFunc {
	if res == nil || res.ownerSessionCount != 0 || res.waiters != 0 {
		return nil
	}
	removed := false
	if existing := c.ongoingArbitraryCalls[res.callKey]; existing != nil && existing.id == res.id {
		delete(c.ongoingArbitraryCalls, res.callKey)
		removed = true
	}
	if existing := c.completedArbitraryCalls[res.callKey]; existing != nil && existing.id == res.id {
		delete(c.completedArbitraryCalls, res.callKey)
		removed = true
	}
	if !removed {
		return nil
	}
	return res.onRelease
}

func runArbitraryOnRelease(ctx context.Context, onRelease OnReleaseFunc) error {
	if onRelease == nil {
		return nil
	}
	return onRelease(context.WithoutCancel(ctx))
}
