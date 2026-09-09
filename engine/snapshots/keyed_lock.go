package snapshots

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

// keyedLocker lets a canceled transfer drop its private pins while another
// transfer continues using the same key. Entries last only while in use.
type keyedLocker struct {
	mu      sync.Mutex
	entries map[string]*keyedLock
}

type keyedLock struct {
	semaphore *semaphore.Weighted
	users     int
}

func (l *keyedLocker) acquire(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[string]*keyedLock)
	}
	entry := l.entries[key]
	if entry == nil {
		entry = &keyedLock{semaphore: semaphore.NewWeighted(1)}
		l.entries[key] = entry
	}
	entry.users++
	l.mu.Unlock()
	remove := func() {
		l.mu.Lock()
		entry.users--
		if entry.users == 0 {
			delete(l.entries, key)
		}
		l.mu.Unlock()
	}
	if err := entry.semaphore.Acquire(ctx, 1); err != nil {
		remove()
		return nil, err
	}
	return func() { entry.semaphore.Release(1); remove() }, nil
}
