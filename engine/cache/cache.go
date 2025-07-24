package cache

import (
	"context"
	"fmt"
	"sync"
)

type Cache[K comparable, V any] interface {
	// Using the given key, either return an already cached value for that key or initialize
	// an entry in the cache with the given value for that key.
	GetOrInitializeValue(context.Context, K, V) (Result[K, V], error)

	// Using the given key, either return an already cached value for that key or initialize a
	// new value using the given function. If the function returns an error, the error is returned.
	GetOrInitialize(
		context.Context,
		K,
		func(context.Context) (V, error),
	) (Result[K, V], error)

	// Using the given key, either return an already cached value for that key or initialize a
	// new value using the given function. If the function returns an error, the error is returned.
	// The function returns a ValueWithCallbacks struct that contains the value and optionally
	// any additional callbacks for various parts of the cache lifecycle.
	GetOrInitializeWithCallbacks(
		context.Context,
		K,
		bool,
		func(context.Context) (*ValueWithCallbacks[V], error),
	) (Result[K, V], error)

	// Returns the number of entries in the cache.
	Size() int
}

type Result[K comparable, V any] interface {
	Result() V
	Release(context.Context) error
	PostCall(context.Context) error
	HitCache() bool
}

type PostCallFunc = func(context.Context) error

type OnReleaseFunc = func(context.Context) error

type ValueWithCallbacks[V any] struct {
	// The actual value to cache
	Value V
	// If set, a function that should be called whenever the value is returned from the cache (whether newly initialized or not)
	PostCall PostCallFunc
	// If set, this function will be called when a result is removed from the cache
	OnRelease OnReleaseFunc
}

var ErrCacheRecursiveCall = fmt.Errorf("recursive call detected")

func NewCache[K comparable, V any]() Cache[K, V] {
	return &cache[K, V]{}
}

type cache[K comparable, V any] struct {
	mu             sync.Mutex
	ongoingCalls   map[K]*result[K, V]
	completedCalls map[K]*result[K, V]
}

var _ Cache[int, int] = &cache[int, int]{}

type result[K comparable, V any] struct {
	cache *cache[K, V]

	key K
	val V
	err error

	postCall  PostCallFunc
	onRelease OnReleaseFunc

	waitCh  chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	refCount int
}

// perCallResult wraps result with metadata that is specific to a single call,
// as opposed to the shared metadata for all instances of a result. e.g. whether
// the result was returned from cache or new.
type perCallResult[K comparable, V any] struct {
	*result[K, V]

	hitCache bool
}

func (r *perCallResult[K, V]) HitCache() bool {
	return r.hitCache
}

var _ Result[int, int] = &perCallResult[int, int]{}

type cacheContextKey[K comparable, V any] struct {
	key   K
	cache *cache[K, V]
}

func (c *cache[K, V]) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.ongoingCalls) + len(c.completedCalls)
}

func (c *cache[K, V]) GetOrInitializeValue(
	ctx context.Context,
	key K,
	val V,
) (Result[K, V], error) {
	return c.GetOrInitialize(ctx, key, func(_ context.Context) (V, error) {
		return val, nil
	})
}

func (c *cache[K, V]) GetOrInitialize(
	ctx context.Context,
	key K,
	fn func(context.Context) (V, error),
) (Result[K, V], error) {
	return c.GetOrInitializeWithCallbacks(ctx, key, false, func(ctx context.Context) (*ValueWithCallbacks[V], error) {
		val, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		return &ValueWithCallbacks[V]{Value: val}, nil
	})
}

func (c *cache[K, V]) GetOrInitializeWithCallbacks(
	ctx context.Context,
	key K,
	skipDedupe bool,
	fn func(context.Context) (*ValueWithCallbacks[V], error),
) (Result[K, V], error) {
	var zeroKey K
	if key == zeroKey {
		// don't cache, don't dedupe calls, just call it
		valWithCallbacks, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		res := &perCallResult[K, V]{result: &result[K, V]{}}
		if valWithCallbacks != nil {
			res.val = valWithCallbacks.Value
			res.postCall = valWithCallbacks.PostCall
			res.onRelease = valWithCallbacks.OnRelease
		}
		return res, nil
	}

	if ctx.Value(cacheContextKey[K, V]{key, c}) != nil {
		return nil, ErrCacheRecursiveCall
	}

	c.mu.Lock()
	if c.ongoingCalls == nil {
		c.ongoingCalls = make(map[K]*result[K, V])
	}
	if c.completedCalls == nil {
		c.completedCalls = make(map[K]*result[K, V])
	}

	if res, ok := c.completedCalls[key]; ok {
		res.refCount++
		c.mu.Unlock()
		return &perCallResult[K, V]{
			result:   res,
			hitCache: true,
		}, nil
	}

	if !skipDedupe {
		if res, ok := c.ongoingCalls[key]; ok {
			// already an ongoing call
			res.waiters++
			c.mu.Unlock()
			return c.wait(ctx, key, res)
		}
	}

	// make a new call with ctx that's only canceled when all caller contexts are canceled
	callCtx := context.WithValue(ctx, cacheContextKey[K, V]{key, c}, struct{}{})
	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	res := &result[K, V]{
		cache: c,

		key: key,

		waitCh:  make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}

	if !skipDedupe {
		c.ongoingCalls[key] = res
	}

	go func() {
		defer close(res.waitCh)
		valWithCallbacks, err := fn(callCtx)
		res.err = err
		if valWithCallbacks != nil {
			res.val = valWithCallbacks.Value
			res.postCall = valWithCallbacks.PostCall
			res.onRelease = valWithCallbacks.OnRelease
		}
	}()

	c.mu.Unlock()
	perCallRes, err := c.wait(ctx, key, res)
	if err != nil {
		return nil, err
	}
	// ensure that this is never marked as hit cache, even in the case
	// where fn returned very quickly and was done by the time wait got
	// called
	perCallRes.hitCache = false
	return perCallRes, nil
}

func (c *cache[K, V]) wait(ctx context.Context, key K, res *result[K, V]) (*perCallResult[K, V], error) {
	var hitCache bool
	var err error

	// first check just if the call is done already, if it is we consider it a cache hit
	select {
	case <-res.waitCh:
		hitCache = true
		err = res.err
	default:
		// call wasn't done in fast path check, wait for either the call to
		// be done or the caller's ctx to be canceled
		select {
		case <-res.waitCh:
			err = res.err
		case <-ctx.Done():
			err = context.Cause(ctx)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	res.waiters--
	if res.waiters == 0 {
		// no one else is waiting, can cancel the callCtx
		res.cancel(err)
	}

	if err == nil {
		delete(c.ongoingCalls, key)
		if existingRes, ok := c.completedCalls[key]; ok {
			res = existingRes
		} else {
			c.completedCalls[key] = res
		}

		res.refCount++
		return &perCallResult[K, V]{
			result:   res,
			hitCache: hitCache,
		}, nil
	}

	if res.refCount == 0 && res.waiters == 0 {
		// error happened and no refs left, delete it now
		delete(c.ongoingCalls, key)
		delete(c.completedCalls, key)
	}
	return nil, err
}

func (res *result[K, V]) Result() V {
	if res == nil {
		var zero V
		return zero
	}
	return res.val
}

func (res *result[K, V]) Release(ctx context.Context) error {
	if res == nil || res.cache == nil {
		// wasn't cached, nothing to do
		return nil
	}

	res.cache.mu.Lock()
	res.refCount--
	var onRelease OnReleaseFunc
	if res.refCount == 0 && res.waiters == 0 {
		// no refs left and no one waiting on it, delete from cache
		delete(res.cache.ongoingCalls, res.key)
		delete(res.cache.completedCalls, res.key)
		onRelease = res.onRelease
	}
	res.cache.mu.Unlock()

	if onRelease != nil {
		return onRelease(ctx)
	}
	return nil
}

func (res *result[K, V]) PostCall(ctx context.Context) error {
	if res.postCall == nil {
		return nil
	}
	return res.postCall(ctx)
}
