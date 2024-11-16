package dagql

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/opencontainers/go-digest"
)

type CacheMap[K comparable, T any] interface {
	GetOrInitialize(context.Context, K, func(context.Context) (T, error)) (T, bool, error)
	Get(context.Context, K) (T, error)
	Keys() []K
}

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

func NewCacheMap[K comparable, T any]() CacheMap[K, T] {
	return newCacheMap[K, T]()
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

func (m *cacheMap[K, T]) GetOrInitialize(ctx context.Context, key K, fn func(ctx context.Context) (T, error)) (T, bool, error) {
	return m.GetOrInitializeOnHit(ctx, key, fn, func(T, error) {})
}

func (m *cacheMap[K, T]) GetOrInitializeOnHit(ctx context.Context, key K, fn func(ctx context.Context) (T, error), onHit func(T, error)) (T, bool, error) {
	if v := ctx.Value(cacheMapContextKey[K, T]{key: key, m: m}); v != nil {
		var zero T
		return zero, false, ErrCacheMapRecursiveCall
	}

	m.l.Lock()
	if c, ok := m.calls[key]; ok {
		m.l.Unlock()
		c.wg.Wait()
		if onHit != nil {
			onHit(c.val, c.err)
		}
		return c.val, true, c.err
	}

	c := &cache[T]{}
	c.wg.Add(1)
	m.calls[key] = c
	m.l.Unlock()

	purity := purityTrackerFromContext(ctx)

	ctx = context.WithValue(ctx, cacheMapContextKey[K, T]{key: key, m: m}, struct{}{})
	c.val, c.err = fn(ctx)
	c.wg.Done()

	if c.err != nil || (purity != nil && purity.IsTainted()) {
		m.l.Lock()
		delete(m.calls, key)
		m.l.Unlock()
	}

	return c.val, false, c.err
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

func (m *cacheMap[K, T]) Keys() []K {
	m.l.Lock()
	keys := make([]K, 0, len(m.calls))
	for k := range m.calls {
		keys = append(keys, k)
	}
	m.l.Unlock()
	return keys
}

type purityTracker struct {
	Pure atomic.Bool
}

type purityKey struct{}

func (t *purityTracker) Taint() {
	t.Pure.Store(true)
}

func (t *purityTracker) IsTainted() bool {
	return t.Pure.Load()
}

// Taint marks the current context as tainted, meaning that the result of the
// current operation is impure and should not be cached.
func Taint(ctx context.Context) {
	if t, ok := ctx.Value(purityKey{}).(*purityTracker); ok {
		t.Taint()
	}
}

func purityTrackerFromContext(ctx context.Context) *purityTracker {
	if val := ctx.Value(purityKey{}); val != nil {
		return val.(*purityTracker)
	}
	return nil
}

func trackPurity(ctx context.Context) (context.Context, *purityTracker) {
	tracker := &purityTracker{}
	ctx = context.WithValue(ctx, purityKey{}, tracker)
	return ctx, tracker
}
