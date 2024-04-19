/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package cacheutil

import (
	"sync"
	"time"
)

// TTLCache is a ttl-based cache with reference counters.
// Each elements is deleted as soon as expiering the configured ttl.
type TTLCache struct {
	m   map[string]*refCounterWithTimer
	mu  sync.Mutex
	ttl time.Duration

	// OnEvicted optionally specifies a callback function to be
	// executed when an entry is purged from the cache.
	OnEvicted func(key string, value interface{})
}

// NewTTLCache creates a new ttl-based cache.
func NewTTLCache(ttl time.Duration) *TTLCache {
	return &TTLCache{
		m:   make(map[string]*refCounterWithTimer),
		ttl: ttl,
	}
}

// Get retrieves the specified object from the cache and increments the reference counter of the
// target content. Client must call `done` callback to decrease the reference count when the value
// will no longer be used.
func (c *TTLCache) Get(key string) (value interface{}, done func(), ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rc, ok := c.m[key]
	if !ok {
		return nil, nil, false
	}
	rc.inc()
	return rc.v, c.decreaseOnceFunc(rc), true
}

// Add adds object to the cache and returns the cached contents with incrementing the reference count.
// If the specified content already exists in the cache, this sets `added` to false and returns
// "already cached" content (i.e. doesn't replace the content with the new one). Client must call
// `done` callback to decrease the counter when the value will no longer be used.
func (c *TTLCache) Add(key string, value interface{}) (cachedValue interface{}, done func(), added bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rc, ok := c.m[key]; ok {
		rc.inc()
		return rc.v, c.decreaseOnceFunc(rc), false
	}
	rc := &refCounterWithTimer{
		refCounter: &refCounter{
			key:       key,
			v:         value,
			onEvicted: c.OnEvicted,
		},
	}
	rc.initialize() // Keep this object having at least 1 ref count (will be decreased in OnEviction)
	rc.inc()        // The client references this object (will be decreased on "done")
	rc.t = time.AfterFunc(c.ttl, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.evictLocked(key)
	})
	c.m[key] = rc
	return rc.v, c.decreaseOnceFunc(rc), true
}

// Remove removes the specified contents from the cache. OnEvicted callback will be called when
// nobody refers to the removed content.
func (c *TTLCache) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictLocked(key)
}

func (c *TTLCache) evictLocked(key string) {
	if rc, ok := c.m[key]; ok {
		delete(c.m, key)
		rc.t.Stop() // stop timer to prevent GC to this content anymore
		rc.finalize()
	}
}

func (c *TTLCache) decreaseOnceFunc(rc *refCounterWithTimer) func() {
	var once sync.Once
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		once.Do(func() { rc.dec() })
	}
}

type refCounterWithTimer struct {
	*refCounter
	t *time.Timer
}
