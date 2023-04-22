package core

import (
	"sync"
)

type cacheMap[K comparable, T any] struct {
	cache map[K]T
	l     sync.Mutex
}

func newCacheMap[K comparable, T any]() *cacheMap[K, T] {
	return &cacheMap[K, T]{
		cache: make(map[K]T),
	}
}

func (cache *cacheMap[K, T]) Get(key K) (T, bool) {
	cache.l.Lock()
	defer cache.l.Unlock()

	val, found := cache.cache[key]
	return val, found
}

type Initializer[T any] interface {
	// Put sets the value and releases the lock on the cache.
	Put(val T)

	// Release will release the lock on the cache.
	Release()
}

// TODO per-key locks
func (cache *cacheMap[K, T]) GetOrInitialize(key K) (T, Initializer[T], bool) {
	cache.l.Lock()

	val, found := cache.cache[key]
	if found {
		cache.l.Unlock()
		return val, nil, true
	}

	// leave l locked until initializer is released
	//
	// TODO: per-key locks

	return val, &cacheInitializer[K, T]{
		key:   key,
		cache: cache,
	}, found
}

func (cache *cacheMap[K, T]) Put(key K, val T) {
	cache.l.Lock()
	defer cache.l.Unlock()

	cache.cache[key] = val
}

type cacheInitializer[K comparable, T any] struct {
	key   K
	cache *cacheMap[K, T]
	done  bool
}

func (init *cacheInitializer[K, T]) Put(val T) {
	init.cache.cache[init.key] = val
	init.done = true
	init.cache.l.Unlock()
}

func (init *cacheInitializer[K, T]) Release() {
	if !init.done {
		init.cache.l.Unlock()
	}
}
