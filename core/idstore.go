package core

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/opencontainers/go-digest"
)

type idStore[T any] struct {
	cache map[digest.Digest]T
	l     sync.Mutex
}

func newIDStore[T any]() *idStore[T] {
	return &idStore[T]{
		cache: make(map[digest.Digest]T),
	}
}

func (cache *idStore[T]) Get(key digest.Digest) (T, error) {
	cache.l.Lock()
	defer cache.l.Unlock()

	val, found := cache.cache[key]
	if !found {
		return val, fmt.Errorf("%T %v not found", key, key)
	}

	return val, nil
}

func (cache *idStore[T]) Put(val T) (digest.Digest, error) {
	cache.l.Lock()
	defer cache.l.Unlock()

	hasher := digest.SHA256.Hash()

	err := json.NewEncoder(hasher).Encode(val)
	if err != nil {
		return "", err
	}

	hash := digest.NewDigest(digest.SHA256, hasher)

	cache.cache[hash] = val

	return hash, nil
}
