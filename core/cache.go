package core

import (
	"crypto/sha256"
	"encoding/base64"

	"github.com/pkg/errors"
)

// Cache is a persistent volume with a globally scoped identifier.
type Cache struct {
	ID CacheID `json:"id"`
}

var ErrInvalidCacheID = errors.New("invalid cache ID; create one using cacheFromTokens")

// CacheID is an arbitrary string typically derived from a set of token
// strings acting as the cache's "key" or "scope".
type CacheID string

func (id CacheID) decode() (*cacheIDPayload, error) {
	var payload cacheIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, ErrInvalidCacheID
	}

	if len(payload.Tokens) == 0 {
		return nil, ErrInvalidCacheID
	}

	return &payload, nil
}

// cacheIDPayload is the inner content of a CacheID.
type cacheIDPayload struct {
	// TODO(vito): right now this is a bit goofy, but if we ever want to add
	// extra server-provided fields, this is where we'd do it.
	Tokens []string `json:"tokens"`
}

// Sum returns a checksum of the cache tokens suitable for use as a cache key.
func (payload cacheIDPayload) Sum() string {
	hash := sha256.New()
	for _, tok := range payload.Tokens {
		_, _ = hash.Write([]byte(tok + "\x00"))
	}

	return base64.StdEncoding.EncodeToString(hash.Sum(nil))
}

func NewCache(tokens []string) (*Cache, error) {
	id, err := encodeID(cacheIDPayload{
		Tokens: tokens,
	})
	if err != nil {
		return nil, err
	}

	return &Cache{
		ID: CacheID(id),
	}, nil
}

func NewCacheFromID(id CacheID) (*Cache, error) {
	_, err := id.decode() // sanity check
	if err != nil {
		return nil, err
	}

	return &Cache{ID: id}, nil
}
