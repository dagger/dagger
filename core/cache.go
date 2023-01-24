package core

import (
	"crypto/sha256"
	"encoding/base64"

	"github.com/pkg/errors"
)

// CacheVolume is a persistent volume with a globally scoped identifier.
type CacheVolume struct {
	ID CacheID `json:"id"`
}

var ErrInvalidCacheID = errors.New("invalid cache ID; create one using cacheVolume")

// CacheID is an arbitrary string typically derived from a set of token
// strings acting as the cache's "key" or "scope".
type CacheID string

// CacheSharingMode is a string deriving from CacheSharingMode enum
// it can take values: SHARED, PRIVATE, LOCKED
type CacheSharingMode string

func (id CacheID) decode() (*cacheIDPayload, error) {
	var payload cacheIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, ErrInvalidCacheID
	}

	if len(payload.Keys) == 0 {
		return nil, ErrInvalidCacheID
	}

	return &payload, nil
}

// cacheIDPayload is the inner content of a CacheID.
type cacheIDPayload struct {
	Keys []string `json:"keys"`
}

// Sum returns a checksum of the cache tokens suitable for use as a cache key.
func (payload cacheIDPayload) Sum() string {
	hash := sha256.New()
	for _, tok := range payload.Keys {
		_, _ = hash.Write([]byte(tok + "\x00"))
	}

	return base64.StdEncoding.EncodeToString(hash.Sum(nil))
}

func (payload cacheIDPayload) Encode() (CacheID, error) {
	id, err := encodeID(payload)
	if err != nil {
		return "", err
	}

	return CacheID(id), nil
}

func NewCache(keys ...string) (*CacheVolume, error) {
	id, err := cacheIDPayload{Keys: keys}.Encode()
	if err != nil {
		return nil, err
	}

	return &CacheVolume{ID: id}, nil
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

	payload.Keys = append(payload.Keys, key)

	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &CacheVolume{ID: id}, nil
}
