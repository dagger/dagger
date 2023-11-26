package core

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/pkg/errors"
)

// CacheVolume is a persistent volume with a globally scoped identifier.
type CacheVolume struct {
	Identified

	Keys []string `json:"keys"`
}

var ErrInvalidCacheVolumeID = errors.New("invalid cache ID; create one using cacheVolume")

func NewCache(keys ...string) *CacheVolume {
	return &CacheVolume{Keys: keys}
}

func (cache *CacheVolume) Clone() *CacheVolume {
	cp := *cache
	cp.Identified.Reset()
	cp.Keys = cloneSlice(cp.Keys)
	return &cp
}

// Sum returns a checksum of the cache tokens suitable for use as a cache key.
func (cache *CacheVolume) Sum() string {
	hash := sha256.New()
	for _, tok := range cache.Keys {
		_, _ = hash.Write([]byte(tok + "\x00"))
	}

	return base64.StdEncoding.EncodeToString(hash.Sum(nil))
}

// CacheSharingMode is a string deriving from CacheSharingMode enum
// it can take values: SHARED, PRIVATE, LOCKED
type CacheSharingMode string

const (
	CacheSharingModeShared  CacheSharingMode = "SHARED"
	CacheSharingModePrivate CacheSharingMode = "PRIVATE"
	CacheSharingModeLocked  CacheSharingMode = "LOCKED"
)

// CacheSharingMode marshals to its lowercased value.
//
// NB: as far as I can recall this is purely for ~*aesthetic*~. GraphQL consts
// are so shouty!
func (mode CacheSharingMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(strings.ToLower(string(mode)))
}

// CacheSharingMode marshals to its lowercased value.
//
// NB: as far as I can recall this is purely for ~*aesthetic*~. GraphQL consts
// are so shouty!
func (mode *CacheSharingMode) UnmarshalJSON(payload []byte) error {
	var str string
	if err := json.Unmarshal(payload, &str); err != nil {
		return err
	}

	*mode = CacheSharingMode(strings.ToUpper(str))

	return nil
}

func (cache *CacheVolume) WithKey(key string) *CacheVolume {
	cache = cache.Clone()
	cache.Keys = append(cache.Keys, key)
	return cache
}
