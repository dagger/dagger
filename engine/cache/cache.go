package cache

import (
	"context"
	"errors"
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
		func(context.Context) (*ValueWithCallbacks[V], error),
	) (Result[K, V], error)
}

type Result[K comparable, V any] interface {
	Result() V
	Release(context.Context) error
	PostCall(context.Context) error
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
	mu    sync.Mutex
	calls map[K]*result[K, V]
}

type result[K comparable, V any] struct {
	cache *cache[K, V]

	key K
	val V
	err error

	postCall  PostCallFunc
	onRelease OnReleaseFunc

	done    chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	refCount int
}

type cacheContextKey[K comparable, V any] struct {
	key   K
	cache *cache[K, V]
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
	return c.GetOrInitializeWithCallbacks(ctx, key, func(ctx context.Context) (*ValueWithCallbacks[V], error) {
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
	fn func(context.Context) (*ValueWithCallbacks[V], error),
) (Result[K, V], error) {
	var zeroKey K
	if key == zeroKey {
		// don't cache, don't dedupe calls, just call it
		valWithCallbacks, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		res := &result[K, V]{}
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
	if c.calls == nil {
		c.calls = make(map[K]*result[K, V])
	}

	if res, ok := c.calls[key]; ok {
		// already an ongoing call
		res.waiters++
		c.mu.Unlock()
		return c.wait(ctx, key, res)
	}

	// make a new call with ctx that's only canceled when all caller contexts are canceled
	callCtx := context.WithValue(ctx, cacheContextKey[K, V]{key, c}, struct{}{})
	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(callCtx))
	res := &result[K, V]{
		cache: c,

		key: key,

		done:    make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}
	c.calls[key] = res
	go func() {
		defer close(res.done)
		valWithCallbacks, err := fn(callCtx)
		res.err = err
		if valWithCallbacks != nil {
			res.val = valWithCallbacks.Value
			res.postCall = valWithCallbacks.PostCall
			res.onRelease = valWithCallbacks.OnRelease
		}
	}()

	c.mu.Unlock()
	return c.wait(ctx, key, res)
}

func (c *cache[K, V]) wait(ctx context.Context, key K, res *result[K, V]) (*result[K, V], error) {
	// wait for either the call to be done or the caller's ctx to be canceled
	var err error
	select {
	case <-res.done:
		err = res.err
	case <-ctx.Done():
		err = context.Cause(ctx)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	res.waiters--
	if res.waiters == 0 {
		// no one else is waiting, can cancel the callCtx
		res.cancel(err)
	}

	if err == nil {
		res.refCount++
		return res, nil
	}

	if res.refCount == 0 {
		// error happened and no refs left, delete it now
		delete(c.calls, key)
	}
	return nil, err
}

func (res *result[K, V]) Result() V {
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
		delete(res.cache.calls, res.key)
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

type CacheWithResults[K comparable, V any] struct {
	cache   Cache[K, V]
	results []Result[K, V]
	mu      sync.Mutex
}

var _ Cache[int, int] = &CacheWithResults[int, int]{}

func NewCacheWithResults[K comparable, V any](baseCache Cache[K, V]) *CacheWithResults[K, V] {
	return &CacheWithResults[K, V]{
		cache: baseCache,
	}
}

func (c *CacheWithResults[K, V]) GetOrInitializeValue(
	ctx context.Context,
	key K,
	val V,
) (Result[K, V], error) {
	return c.GetOrInitialize(ctx, key, func(_ context.Context) (V, error) {
		return val, nil
	})
}

func (c *CacheWithResults[K, V]) GetOrInitialize(
	ctx context.Context,
	key K,
	fn func(context.Context) (V, error),
) (Result[K, V], error) {
	return c.GetOrInitializeWithCallbacks(ctx, key, func(ctx context.Context) (*ValueWithCallbacks[V], error) {
		val, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		return &ValueWithCallbacks[V]{Value: val}, nil
	})
}

func (c *CacheWithResults[K, V]) GetOrInitializeWithCallbacks(
	ctx context.Context,
	key K,
	fn func(context.Context) (*ValueWithCallbacks[V], error),
) (Result[K, V], error) {
	res, err := c.cache.GetOrInitializeWithCallbacks(ctx, key, fn)

	var zeroKey K
	if res != nil && key != zeroKey {
		c.mu.Lock()
		c.results = append(c.results, res)
		c.mu.Unlock()
	}

	return res, err
}

func (c *CacheWithResults[K, V]) ReleaseAll(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var rerr error
	for _, res := range c.results {
		rerr = errors.Join(rerr, res.Release(ctx))
	}
	c.results = nil

	return rerr
}
