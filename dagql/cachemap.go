package dagql

import (
	"context"
	"fmt"
	"sync"

	"github.com/opencontainers/go-digest"
)

type cacheMap[K comparable, T any] struct {
	l     sync.Mutex
	calls map[K]*cache[T]
}

type cache[T any] struct {
	wg  sync.WaitGroup
	val T
	err error
}

// NewCache creates a new cache map suitable for assigning on a Server or
// multiple Servers.
func NewCache() Cache {
	return newCacheMap[digest.Digest, Typed]()
}

func newCacheMap[K comparable, T any]() *cacheMap[K, T] {
	return &cacheMap[K, T]{
		calls: map[K]*cache[T]{},
	}
}

type cacheMapContextKey[K comparable, T any] struct {
	key K
	m   *cacheMap[K, T]
}

var ErrCacheMapRecursiveCall = fmt.Errorf("recursive call detected")

func (m *cacheMap[K, T]) Set(key K, val T) {
	m.l.Lock()
	m.calls[key] = &cache[T]{
		val: val,
	}
	m.l.Unlock()
}

func (m *cacheMap[K, T]) GetOrInitialize(ctx context.Context, key K, fn func(ctx context.Context) (T, error)) (T, error) {
	return m.GetOrInitializeOnHit(ctx, key, fn, func(T, error) {})
}

func (m *cacheMap[K, T]) GetOrInitializeOnHit(ctx context.Context, key K, fn func(ctx context.Context) (T, error), onHit func(T, error)) (T, error) {
	if v := ctx.Value(cacheMapContextKey[K, T]{key: key, m: m}); v != nil {
		var zero T
		return zero, ErrCacheMapRecursiveCall
	}

	m.l.Lock()
	if c, ok := m.calls[key]; ok {
		m.l.Unlock()
		c.wg.Wait()
		if onHit != nil {
			onHit(c.val, c.err)
		}
		return c.val, c.err
	}

	c := &cache[T]{}
	c.wg.Add(1)
	m.calls[key] = c
	m.l.Unlock()

	ctx = context.WithValue(ctx, cacheMapContextKey[K, T]{key: key, m: m}, struct{}{})
	c.val, c.err = fn(ctx)
	c.wg.Done()

	if c.err != nil {
		m.l.Lock()
		delete(m.calls, key)
		m.l.Unlock()
	}

	return c.val, c.err
}

func (m *cacheMap[K, T]) Get(ctx context.Context, key K) (T, error) {
	if v := ctx.Value(cacheMapContextKey[K, T]{key: key, m: m}); v != nil {
		var zero T
		return zero, ErrCacheMapRecursiveCall
	}

	m.l.Lock()
	if c, ok := m.calls[key]; ok {
		m.l.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	m.l.Unlock()

	var zero T
	return zero, fmt.Errorf("key not found")
}
