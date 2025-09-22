package core

import (
	"strconv"
	"sync"
	"time"

	"github.com/dagger/dagger/dagql"
)

type CallExpirationCache struct {
	cache sync.Map
}

type expirationCacheEntry struct {
	mixin      string
	expiration int64
}

func NewCallExpirationCache() *CallExpirationCache {
	return &CallExpirationCache{}
}

func (f *CallExpirationCache) GetOrInitExpiration(key string, ttl int64, sessionID string) (*expirationCacheEntry, func()) {
	now := time.Now().Unix()
	newExpiration := now + ttl

	v, loaded := f.cache.Load(key)
	var existingEntry *expirationCacheEntry
	if loaded {
		existingEntry = v.(*expirationCacheEntry)
	}

	switch {
	case !loaded:
		// Nothing saved in the cache yet, use a new mixin. Don't store yet, that only happens
		// once a call completes successfully and has been determined to be safe to cache.
		newEntry := newExpirationEntry(newExpiration, sessionID)
		return newEntry, func() {
			f.cache.LoadOrStore(key, newEntry)
		}

	case existingEntry.expiration < now:
		// We do have a cached entry, but it expired, so don't use it. Use a new mixin, but again
		// don't store it yet until the call completes successfully and is determined to be safe
		// to cache.
		newEntry := newExpirationEntry(newExpiration, sessionID)
		return newEntry, func() {
			// Delete the old expired entry, provided no one else already updated it
			deleted := f.cache.CompareAndDelete(key, existingEntry)
			if deleted {
				f.cache.LoadOrStore(key, newEntry)
			}
		}

	default:
		// We have a cached entry and it hasn't expired yet, use the cached mixin
		return existingEntry, func() {}
	}
}

func newExpirationEntry(newExpiration int64, sessionID string) *expirationCacheEntry {
	return &expirationCacheEntry{
		mixin:      dagql.HashFrom(strconv.Itoa(int(newExpiration)), sessionID).String(),
		expiration: int64(newExpiration),
	}
}

func (f *CallExpirationCache) GCLoop() {
	now := time.Now().Unix()
	for range time.Tick(10 * time.Minute) {
		f.cache.Range(func(key, value any) bool {
			entry := value.(*expirationCacheEntry)
			if entry.expiration < now {
				f.cache.CompareAndDelete(key, entry)
			}
			return true
		})
	}
}
