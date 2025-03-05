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
	// The function can also return an optional post call function that will be set on the returned
	// result so callers of this function can call it when post-processing of all results (cached or
	// not) is needed.
	GetOrInitializeWithPostCall(
		context.Context,
		K,
		func(context.Context) (V, PostCallFunc, error),
	) (Result[K, V], error)
}

type Result[K comparable, V any] interface {
	Result() V
	Release()
	PostCall(context.Context) error
}

type PostCallFunc = func(context.Context) error

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

	key      K
	val      V
	postCall PostCallFunc
	err      error

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
	return c.GetOrInitializeWithPostCall(ctx, key, func(ctx context.Context) (V, PostCallFunc, error) {
		val, err := fn(ctx)
		return val, nil, err
	})
}

func (c *cache[K, V]) GetOrInitializeWithPostCall(
	ctx context.Context,
	key K,
	fn func(context.Context) (V, PostCallFunc, error),
) (Result[K, V], error) {
	var zeroKey K
	if key == zeroKey {
		// don't cache, don't dedupe calls, just call it
		res := &result[K, V]{}
		res.val, res.postCall, res.err = fn(ctx)
		return res, res.err
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
		res.val, res.postCall, res.err = fn(callCtx)
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

func (res *result[K, V]) Release() {
	if res.cache == nil {
		// wasn't cached, nothing to do
		return
	}

	res.cache.mu.Lock()
	defer res.cache.mu.Unlock()

	res.refCount--
	if res.refCount == 0 && res.waiters == 0 {
		// no refs left and no one waiting on it, delete from cache
		delete(res.cache.calls, res.key)
	}
}

func (res *result[K, V]) PostCall(ctx context.Context) error {
	if res.postCall == nil {
		return nil
	}
	return res.postCall(ctx)
}
