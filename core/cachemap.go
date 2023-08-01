package core

import (
	"fmt"
	"sync"

	"github.com/zeebo/xxh3"
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

func newCacheMap[K comparable, T any]() *cacheMap[K, T] {
	return &cacheMap[K, T]{
		calls: map[K]*cache[T]{},
	}
}

func (m *cacheMap[K, T]) GetOrInitialize(key K, fn func() (T, error)) (T, error) {
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
