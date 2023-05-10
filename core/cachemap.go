package core

import (
	"sync"
)

type cacheMap[K comparable, T any] struct {
	cache map[K]*cacheInitializer[K, T]
	l     sync.Mutex
}

func newCacheMap[K comparable, T any]() *cacheMap[K, T] {
	return &cacheMap[K, T]{
		cache: make(map[K]*cacheInitializer[K, T]),
	}
}

func (cache *cacheMap[K, T]) Get(key K) (T, bool) {
	cache.l.Lock()
	defer cache.l.Unlock()

	init, found := cache.cache[key]
	if found {
		init.Wait()
		return init.val, true
	}

	var zero T
	return zero, false
}

type Initializer[T any] interface {
	// Put sets the value and wakes up any callers waiting for it.
	Put(val T)

	// Release removes the initializer from the cache if it has not been
	// initialized.
	Release()
}

func (cache *cacheMap[K, T]) GetOrInitialize(key K) (T, Initializer[T], bool) {
	cache.l.Lock()
	defer cache.l.Unlock()

	init, found := cache.cache[key]
	if found {
		init.Wait()
		return init.val, nil, true
	}

	init = &cacheInitializer[K, T]{
		wg:    new(sync.WaitGroup),
		key:   key,
		cache: cache,
	}

	init.wg.Add(1)

	cache.cache[key] = init

	var zero T
	return zero, init, found
}

type cacheInitializer[K comparable, T any] struct {
	wg    *sync.WaitGroup
	key   K
	val   T
	cache *cacheMap[K, T]
	done  bool
}

func (init *cacheInitializer[K, T]) Wait() {
	init.wg.Wait()
}

func (init *cacheInitializer[K, T]) Put(val T) {
	init.val = val
	init.done = true
	init.wg.Done()
}

func (init *cacheInitializer[K, T]) Release() {
	if !init.done {
		init.cache.l.Lock()
		delete(init.cache.cache, init.key)
		init.cache.l.Unlock()
	}
}
