package snapshots

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

// exportLocker lets a canceled exporter drop its private pins while another
// exporter continues using the same snapshot. Entries last only while in use.
type exportLocker struct {
	mu      sync.Mutex
	entries map[string]*exportLock
}

type exportLock struct {
	semaphore *semaphore.Weighted
	users     int
}

func (l *exportLocker) acquire(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[string]*exportLock)
	}
	entry := l.entries[key]
	if entry == nil {
		entry = &exportLock{semaphore: semaphore.NewWeighted(1)}
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
