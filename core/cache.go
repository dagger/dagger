package core

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/pkg/errors"
)

// CacheVolume is a persistent volume with a globally scoped identifier.
type CacheVolume struct {
	ID CacheID `json:"id"`
}

var ErrInvalidCacheID = errors.New("invalid cache ID; create one using cache.withKey")

// CacheID is an arbitrary string typically derived from a set of token
// strings acting as the cache's "key" or "scope".
type CacheID string

func (id CacheID) decode() (*cacheIDPayload, error) {
	var payload cacheIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, ErrInvalidCacheID
	}

	if payload.Key == "" {
		return nil, ErrInvalidCacheID
	}

	return &payload, nil
}

// cacheIDPayload is the inner content of a CacheID.
type cacheIDPayload struct {
	// TODO(vito): right now this is a bit goofy, but if we ever want to add
	// extra fields for scoping, this is what we'd augment.
	Key string `json:"key"`
}

// Sum returns a checksum of the cache tokens suitable for use as a cache key.
func (payload cacheIDPayload) Sum() string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(payload.Key))
	return base64.StdEncoding.EncodeToString(hash.Sum(nil))
}

func NewCache(key string) (*CacheVolume, error) {
	id, err := encodeID(cacheIDPayload{
		Key: key,
	})
	if err != nil {
		return nil, err
	}

	return &CacheVolume{
		ID: CacheID(id),
	}, nil
}

func NewCacheFromID(id CacheID) (*CacheVolume, error) {
	_, err := id.decode() // sanity check
	if err != nil {
		return nil, err
	}

	return &CacheVolume{ID: id}, nil
}

func (cache *CacheVolume) WithKey(key string) (*CacheVolume, error) {
	payload, err := cache.ID.decode()
	if err != nil {
		return nil, err
	}

	return NewCache(fmt.Sprintf("%s:%s", payload.Key, key))
}
