package dagql

import (
	"context"
	"fmt"
	"sync"

	"github.com/opencontainers/go-digest"
)

// TODO: DEDUPE W/ local.SingleflightGroup
// TODO: DEDUPE W/ local.SingleflightGroup
// TODO: DEDUPE W/ local.SingleflightGroup
// TODO: DEDUPE W/ local.SingleflightGroup

// Cache stores results of pure selections against Server.
type Cache interface {
	GetOrInitialize(
		context.Context,
		digest.Digest,
		func(context.Context) (Typed, error),
	) (*CachedResult[digest.Digest, Typed], error)
	GetOrInitializeValue(context.Context, digest.Digest, Typed) (*CachedResult[digest.Digest, Typed], error)
}

// NewCache creates a new cache map suitable for assigning on a Server or
// multiple Servers.
func NewCache() Cache {
	return newCache[digest.Digest, Typed]()
}

func newCache[K comparable, V any]() *cache[K, V] {
	return &cache[K, V]{
		calls: map[K]*CachedResult[K, V]{},
	}
}

type cache[K comparable, V any] struct {
	mu    sync.Mutex
	calls map[K]*CachedResult[K, V]
}

type CachedResult[K comparable, V any] struct {
	cache *cache[K, V]

	key K
	val V
	err error

	done    chan struct{}
	cancel  context.CancelCauseFunc
	waiters int

	refCount int
}

type cacheContextKey[K comparable, V any] struct {
	key   K
	cache *cache[K, V]
}

var ErrCacheMapRecursiveCall = fmt.Errorf("recursive call detected")

func (c *cache[K, V]) GetOrInitializeValue(ctx context.Context, key K, v V) (*CachedResult[K, V], error) {
	return c.GetOrInitialize(ctx, key, func(context.Context) (V, error) {
		return v, nil
	})
}

func (c *cache[K, V]) GetOrInitialize(ctx context.Context, key K, fn func(ctx context.Context) (V, error)) (*CachedResult[K, V], error) {
	if v := ctx.Value(cacheContextKey[K, V]{key: key, cache: c}); v != nil {
		return nil, ErrCacheMapRecursiveCall
	}

	c.mu.Lock()
	if c.calls == nil {
		c.calls = make(map[K]*CachedResult[K, V])
	}

	if res, ok := c.calls[key]; ok {
		res.waiters++
		c.mu.Unlock()
		return c.wait(ctx, key, res)
	}

	callCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	callCtx = context.WithValue(callCtx, cacheContextKey[K, V]{key: key, cache: c}, struct{}{})
	res := &CachedResult[K, V]{
		cache: c,

		key: key,

		done:    make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}
	c.calls[key] = res

	// TODO: this is an extra 1 goroutine per call than the previous cachemap impl, does that matter?
	go func() {
		defer close(res.done)
		res.val, res.err = fn(callCtx)
	}()

	c.mu.Unlock()
	return c.wait(ctx, key, res)
}

func (c *cache[K, V]) wait(ctx context.Context, key K, res *CachedResult[K, V]) (*CachedResult[K, V], error) {
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
		res.cancel(err)
	}

	if err == nil {
		res.refCount++
		return res, nil
	}

	if res.refCount == 0 {
		delete(c.calls, key)
	}
	return nil, err
}

func (res *CachedResult[K, V]) Result() V {
	return res.val
}

func (res *CachedResult[K, V]) Release() {
	res.cache.mu.Lock()
	defer res.cache.mu.Unlock()

	res.refCount--
	if res.refCount == 0 && res.waiters == 0 {
		delete(res.cache.calls, res.key)
	}
}

// TODO: doc
type CacheWithResults struct {
	Cache
	results map[*CachedResult[digest.Digest, Typed]]struct{}
	mu      sync.Mutex
}

func NewCacheWithResults(baseCache Cache) *CacheWithResults {
	return &CacheWithResults{
		Cache:   baseCache,
		results: map[*CachedResult[digest.Digest, Typed]]struct{}{},
	}
}

func (c *CacheWithResults) GetOrInitializeValue(
	ctx context.Context,
	key digest.Digest,
	v Typed,
) (*CachedResult[digest.Digest, Typed], error) {
	return c.Cache.GetOrInitialize(ctx, key, func(context.Context) (Typed, error) {
		return v, nil
	})
}

func (c *CacheWithResults) GetOrInitialize(
	ctx context.Context,
	key digest.Digest,
	fn func(context.Context) (Typed, error),
) (*CachedResult[digest.Digest, Typed], error) {
	res, err := c.Cache.GetOrInitialize(ctx, key, fn)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.results[res] = struct{}{}

	return res, nil
}

func (c *CacheWithResults) Release() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for res := range c.results {
		res.Release()
	}
	c.results = map[*CachedResult[digest.Digest, Typed]]struct{}{}
}
