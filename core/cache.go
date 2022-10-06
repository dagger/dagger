package core

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
)

// Cache is a persistent volume with a globally scoped identifier.
type Cache struct {
	ID CacheID `json:"id"`
}

// CacheID is an arbitrary string typically derived from a set of token
// strings acting as the cache's "key" or "scope".
type CacheID string

// cacheIDPayload is the inner content of a CacheID.
type cacheIDPayload struct {
	// TODO(vito): right now this is a bit goofy, but if we ever want to add
	// extra server-provided fields, this is where we'd do it.
	Tokens []string `json:"tokens"`
}

func NewCache(tokens []string) (*Cache, error) {
	id, err := hashID(cacheIDPayload{
		Tokens: tokens,
	})
	if err != nil {
		return nil, err
	}

	return &Cache{
		ID: CacheID(id),
	}, nil
}

func hashID(payload any) (string, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	hash := sha256.New()
	_, err = hash.Write(jsonBytes)
	if err != nil {
		return "", err
	}

	sum := hash.Sum(nil)
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}
