package core

import (
	"sync"
	"time"
)

type CallExpirationCache struct {
	cache sync.Map
}

func NewCallExpirationCache() *CallExpirationCache {
	return &CallExpirationCache{}
}

func (f *CallExpirationCache) GetOrInitExpiration(key string, ttl int64) int64 {
	now := time.Now().Unix()
	newExpiration := now + ttl

	// load current expiration, if hasn't past or wasn't set, return it
	v, _ := f.cache.LoadOrStore(key, newExpiration)
	existingExpiration := v.(int64)
	if existingExpiration > now {
		return existingExpiration
	}

	// it expired, try to swap in new expiration time
	swapped := f.cache.CompareAndSwap(key, existingExpiration, newExpiration)
	if swapped {
		// swapped in successfully, return new expiration time
		return newExpiration
	}

	// We lost a race to reset the expiration time, return whatever is there now (it
	// should be close enough to what we wanted). Do a LoadOrStore in case someone
	// did a delete though.
	v, _ = f.cache.LoadOrStore(key, newExpiration)
	return v.(int64)
}

func (f *CallExpirationCache) UnsetExpiration(key string, expectedExpiration int64) {
	f.cache.CompareAndDelete(key, expectedExpiration)
}

func (f *CallExpirationCache) GCLoop() {
	now := time.Now().Unix()
	for range time.Tick(10 * time.Minute) {
		f.cache.Range(func(key, value any) bool {
			expiration := value.(int64)
			if expiration < now {
				f.cache.CompareAndDelete(key, expiration)
			}
			return true
		})
	}
}
