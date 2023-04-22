package core

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/zeebo/xxh3"
)

type idStore[K ~string, T any] struct {
	cache map[K]T
	l     sync.Mutex
}

func newIDStore[K ~string, T any]() *idStore[K, T] {
	return &idStore[K, T]{
		cache: make(map[K]T),
	}
}

func (cache *idStore[K, T]) Get(key K) (T, error) {
	cache.l.Lock()
	defer cache.l.Unlock()

	val, found := cache.cache[key]
	if !found {
		return val, fmt.Errorf("%T %v not found", key, key)
	}

	return val, nil
}

func (cache *idStore[K, T]) Put(val T) (K, error) {
	cache.l.Lock()
	defer cache.l.Unlock()

	hasher := xxh3.New()

	err := json.NewEncoder(hasher).Encode(val)
	if err != nil {
		return "", err
	}

	hash := K(b32(hasher.Sum64()))

	cache.cache[hash] = val

	return hash, nil
}
