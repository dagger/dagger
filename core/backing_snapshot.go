package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// lateBackingSnapshot is a backing value whose snapshot can be created after
// its row was published: a row imported as foreign_uninitialized arrives with
// no snapshot and makes a local one at first use.
type lateBackingSnapshot interface {
	dagql.Typed
	// ensureBackingSnapshot creates or reopens the snapshot. It reports whether
	// the row's owner leases may not cover it yet.
	ensureBackingSnapshot(ctx context.Context) (syncOwed bool, err error)
}

// EnsureBackingSnapshot makes row's snapshot exist and owned by row. Every use
// of a CacheVolume, RemoteGitMirror or ClientFilesyncMirror row that may create
// the snapshot goes through here: a snapshot created after publication is
// covered only by the calling session's lease, so without the owner lease a
// collection after that session ends removes it under the live value, and the
// next checkpoint saves a link that fails the following boot. A failed sync
// fails the call, as it does for HTTPState, and is retried by the next use.
func EnsureBackingSnapshot[T lateBackingSnapshot](ctx context.Context, row dagql.ObjectResult[T]) error {
	syncOwed, err := row.Self().ensureBackingSnapshot(ctx)
	if err != nil || !syncOwed {
		return err
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.SyncResultSnapshotOwnerLeases(ctx, row); err != nil {
		return fmt.Errorf("sync %s snapshot owner leases: %w", row.Self().Type().Name(), err)
	}
	return nil
}

func (cache *CacheVolume) ensureBackingSnapshot(ctx context.Context) (bool, error) {
	created := cache.getSnapshot() == nil
	if created {
		if err := cache.InitializeSnapshot(ctx); err != nil {
			return false, err
		}
	}
	return created || cache.foreignUninitialized, nil
}

func (mirror *RemoteGitMirror) ensureBackingSnapshot(ctx context.Context) (bool, error) {
	mirror.mu.Lock()
	defer mirror.mu.Unlock()
	created := mirror.snapshot == nil
	if created {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return false, err
		}
		if err := mirror.ensureSnapshotLocked(ctx, query); err != nil {
			return false, err
		}
	}
	return created || mirror.foreignUninitialized, nil
}

func (m *ClientFilesyncMirror) ensureBackingSnapshot(ctx context.Context) (bool, error) {
	m.mu.Lock()
	created := m.snapshot == nil
	m.mu.Unlock()
	if created {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return false, err
		}
		if err := m.EnsureCreated(ctx, query); err != nil {
			return false, err
		}
	}
	return created || m.foreignUninitialized, nil
}
