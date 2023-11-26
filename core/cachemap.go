package core

import (
	"fmt"
	"sync"

	"github.com/zeebo/xxh3"
)

type CacheMap[K comparable, T any] struct {
	l     sync.Mutex
	calls map[K]*cache[T]
}

type cache[T any] struct {
	wg  sync.WaitGroup
	val T
	err error
}

func cacheKey(keys ...any) uint64 {
	hash := xxh3.New()

	for _, key := range keys {
		dig, err := stableDigest(key)
		if err != nil {
			panic(err)
		}
		fmt.Fprintln(hash, dig)
	}

	return hash.Sum64()
}

func NewCacheMap[K comparable, T any]() *CacheMap[K, T] {
	return &CacheMap[K, T]{
		calls: map[K]*cache[T]{},
	}
}

func (m *CacheMap[K, T]) Get(key K) (T, error) {
	m.l.Lock()
	if c, ok := m.calls[key]; ok {
		m.l.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	m.l.Unlock()
	var zero T
	return zero, fmt.Errorf("cache key %v not found", key)
}

func (m *CacheMap[K, T]) Set(key K, val T) {
	m.l.Lock()
	m.calls[key] = &cache[T]{
		val: val,
	}
	m.l.Unlock()
}

func (m *CacheMap[K, T]) GetOrInitialize(key K, fn func() (T, error)) (T, error) {
	m.l.Lock()
	if c, ok := m.calls[key]; ok {
		m.l.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}

	c := &cache[T]{}
	c.wg.Add(1)
	m.calls[key] = c
	m.l.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	if c.err != nil {
		m.l.Lock()
		delete(m.calls, key)
		m.l.Unlock()
	}

	return c.val, c.err
}
