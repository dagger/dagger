package core

import (
	"crypto/sha256"
	"encoding/base64"

	"github.com/pkg/errors"
)

// CacheVolume is a persistent volume with a globally scoped identifier.
type CacheVolume struct {
	Keys []string `json:"keys"`
}

var ErrInvalidCacheID = errors.New("invalid cache ID; create one using cacheVolume")

func NewCache(keys ...string) *CacheVolume {
	return &CacheVolume{Keys: keys}
}

func (cache *CacheVolume) Clone() *CacheVolume {
	cp := *cache
	cp.Keys = cloneSlice(cp.Keys)
	return &cp
}

// CacheID is an arbitrary string typically derived from a set of token
// strings acting as the cache's "key" or "scope".
type CacheID string

func (id CacheID) ToCacheVolume() (*CacheVolume, error) {
	var cache CacheVolume
	if err := decodeID(&cache, id); err != nil {
		return nil, ErrInvalidCacheID
	}

	if len(cache.Keys) == 0 {
		return nil, ErrInvalidCacheID
	}

	return &cache, nil
}

// Sum returns a checksum of the cache tokens suitable for use as a cache key.
func (cache *CacheVolume) Sum() string {
	hash := sha256.New()
	for _, tok := range cache.Keys {
		_, _ = hash.Write([]byte(tok + "\x00"))
	}

	return base64.StdEncoding.EncodeToString(hash.Sum(nil))
}

func (cache *CacheVolume) ID() (CacheID, error) {
	return encodeID[CacheID](cache)
}

// CacheSharingMode is a string deriving from CacheSharingMode enum
// it can take values: SHARED, PRIVATE, LOCKED
type CacheSharingMode string

const (
	CacheSharingModeShared  CacheSharingMode = "SHARED"
	CacheSharingModePrivate CacheSharingMode = "PRIVATE"
	CacheSharingModeLocked  CacheSharingMode = "LOCKED"
)

func (cache *CacheVolume) WithKey(key string) *CacheVolume {
	cache = cache.Clone()
	cache.Keys = append(cache.Keys, key)
	return cache
}
