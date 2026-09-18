package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql"
)

// lateBackingSnapshot is a backing value whose snapshot can be created after
// its row was published: a row imported as foreign_uninitialized arrives with
// no snapshot and makes a local one at first use.
type lateBackingSnapshot interface {
	dagql.Typed
	// ensureBackingSnapshot creates or reopens the snapshot. It reports whether
	// the row's owner leases may not cover it yet, and, when this call created
	// the snapshot, how to drop it again and return the value to the state it
	// had before the call.
	ensureBackingSnapshot(ctx context.Context) (syncOwed bool, discard func(context.Context) error, err error)
	// backingSnapshotMu serializes EnsureBackingSnapshot per value. It is the
	// helper's own lock, not the value's state lock: the lease sync reads the
	// value's links through the state lock, and the encoder takes the state
	// lock too, so neither ever waits on a lease-manager call.
	backingSnapshotMu() *sync.Mutex
}

// EnsureBackingSnapshot makes row's snapshot exist and owned by row. Every use
// of a CacheVolume, RemoteGitMirror or ClientFilesyncMirror row that may create
// the snapshot goes through here: a snapshot created after publication is
// covered only by the calling session's lease, so without the owner lease a
// collection after that session ends removes it under the live value, and the
// next checkpoint saves a link that fails the following boot.
//
// A failed sync fails the call, as it does for HTTPState, and a snapshot this
// call created is dropped before returning: the value goes back to having no
// snapshot, reports no link, and the next use creates and syncs again. Keeping
// the snapshot would leave it protected by the session alone while the value
// already reports its link, which is the fault this helper exists to prevent.
//
// Creation, sync and discard are one step per value: concurrent first uses
// queue on the value's backing lock, so a second caller never sees a snapshot
// that the first is about to drop, and never syncs a snapshot half made.
func EnsureBackingSnapshot[T lateBackingSnapshot](ctx context.Context, row dagql.ObjectResult[T]) error {
	mu := row.Self().backingSnapshotMu()
	mu.Lock()
	defer mu.Unlock()
	syncOwed, discard, err := row.Self().ensureBackingSnapshot(ctx)
	if err != nil || !syncOwed {
		return err
	}
	cache, err := dagql.EngineCache(ctx)
	if err == nil {
		err = cache.SyncResultSnapshotOwnerLeases(ctx, row)
	}
	if err == nil {
		return nil
	}
	err = fmt.Errorf("sync %s snapshot owner leases: %w", row.Self().Type().Name(), err)
	if discard != nil {
		if discardErr := discard(context.WithoutCancel(ctx)); discardErr != nil {
			err = errors.Join(err, fmt.Errorf("discard unowned %s snapshot: %w", row.Self().Type().Name(), discardErr))
		}
	}
	return err
}

func (cache *CacheVolume) ensureBackingSnapshot(ctx context.Context) (bool, func(context.Context) error, error) {
	cache.mu.Lock()
	opened := cache.snapshot == nil
	created := opened && cache.snapshotID == ""
	cache.mu.Unlock()
	if opened {
		if err := cache.InitializeSnapshot(ctx); err != nil {
			return false, nil, err
		}
	}
	var discard func(context.Context) error
	if created {
		discard = func(ctx context.Context) error {
			cache.mu.Lock()
			snapshot, releaseSnapshot := cache.snapshot, cache.releaseSnapshot
			cache.snapshot, cache.releaseSnapshot, cache.snapshotID = nil, nil, ""
			cache.mu.Unlock()
			if releaseSnapshot != nil {
				return releaseSnapshot(ctx)
			}
			if snapshot != nil {
				return snapshot.Release(ctx)
			}
			return nil
		}
	}
	return opened || cache.foreignUninitialized, discard, nil
}

func (mirror *RemoteGitMirror) ensureBackingSnapshot(ctx context.Context) (bool, func(context.Context) error, error) {
	mirror.mu.Lock()
	defer mirror.mu.Unlock()
	created := mirror.snapshot == nil
	var discard func(context.Context) error
	if created {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return false, nil, err
		}
		if err := mirror.ensureSnapshotLocked(ctx, query); err != nil {
			return false, nil, err
		}
		discard = func(ctx context.Context) error {
			mirror.mu.Lock()
			snapshot := mirror.snapshot
			mirror.snapshot = nil
			mirror.mu.Unlock()
			if snapshot == nil {
				return nil
			}
			return snapshot.Release(ctx)
		}
	}
	return created || mirror.foreignUninitialized, discard, nil
}

func (m *ClientFilesyncMirror) ensureBackingSnapshot(ctx context.Context) (bool, func(context.Context) error, error) {
	m.mu.Lock()
	created := m.snapshot == nil
	m.mu.Unlock()
	var discard func(context.Context) error
	if created {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return false, nil, err
		}
		if err := m.EnsureCreated(ctx, query); err != nil {
			return false, nil, err
		}
		discard = func(ctx context.Context) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.sharedState != nil {
				// a mount is in use; the runtime release drops the mount but
				// the snapshot must stay under it until then
				return fmt.Errorf("client filesync mirror snapshot is mounted")
			}
			snapshot := m.snapshot
			m.snapshot = nil
			if snapshot == nil {
				return nil
			}
			return snapshot.Release(ctx)
		}
	}
	return created || m.foreignUninitialized, discard, nil
}

func (cache *CacheVolume) backingSnapshotMu() *sync.Mutex      { return &cache.backingMu }
func (mirror *RemoteGitMirror) backingSnapshotMu() *sync.Mutex { return &mirror.backingMu }
func (m *ClientFilesyncMirror) backingSnapshotMu() *sync.Mutex { return &m.backingMu }
