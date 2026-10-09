package core

import (
	"context"
	"errors"
	"sync"

	"github.com/containerd/containerd/v2/core/mount"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

type readMountScopeKey struct{}

// readMountScope lets the reads made for one call share a read-only mount of
// each snapshot they read, such as the stat that resolves a subfile and the
// read of its contents. Every read-only mount of a snapshot creates and
// discards a view and a lease, so sharing one saves that work for each read
// after the first. The mounts are released when the scope is closed.
type readMountScope struct {
	mu     sync.Mutex
	mounts map[string]*scopedReadMount
	closed bool
}

type scopedReadMount struct {
	// users counts the readers using or mounting it; guarded by the scope's mu.
	users int

	mu      sync.Mutex
	mounted bool
	dir     string
	m       *mount.Mount
	closer  func() error
}

// withReadMountScope returns a context whose shared read-only mounts (see
// mountRefShared) last until the returned close is called. Within an
// existing scope it returns that scope and a close that does nothing.
func withReadMountScope(ctx context.Context) (context.Context, func() error) {
	if readMountScopeFrom(ctx) != nil {
		return ctx, func() error { return nil }
	}
	scope := &readMountScope{mounts: map[string]*scopedReadMount{}}
	return context.WithValue(ctx, readMountScopeKey{}, scope), scope.close
}

func readMountScopeFrom(ctx context.Context) *readMountScope {
	scope, _ := ctx.Value(readMountScopeKey{}).(*readMountScope)
	return scope
}

// mountShared runs f on the scope's read-only mount of ref's snapshot,
// mounting it first if no reader has. It reports false, without running f,
// once the scope is closed.
func (scope *readMountScope) mountShared(ctx context.Context, ref bkcache.ImmutableRef, f func(string, *mount.Mount) error) (bool, error) {
	id := ref.SnapshotID()
	scope.mu.Lock()
	if scope.closed {
		scope.mu.Unlock()
		return false, nil
	}
	entry := scope.mounts[id]
	if entry == nil {
		entry = &scopedReadMount{}
		scope.mounts[id] = entry
	}
	entry.users++
	scope.mu.Unlock()

	err := entry.use(ctx, ref, f)
	return true, errors.Join(err, scope.release(entry))
}

func (entry *scopedReadMount) use(ctx context.Context, ref bkcache.ImmutableRef, f func(string, *mount.Mount) error) error {
	entry.mu.Lock()
	if !entry.mounted {
		dir, m, closer, err := MountRefCloser(ctx, ref, mountRefAsReadOnly)
		if err != nil {
			// The next reader mounts it again.
			entry.mu.Unlock()
			return err
		}
		entry.mounted, entry.dir, entry.m, entry.closer = true, dir, m, closer
	}
	dir, m := entry.dir, entry.m
	entry.mu.Unlock()
	return f(dir, m)
}

// release ends one reader's use of entry. The last reader of a closed scope
// unmounts it.
func (scope *readMountScope) release(entry *scopedReadMount) error {
	scope.mu.Lock()
	entry.users--
	unmount := scope.closed && entry.users == 0
	scope.mu.Unlock()
	if unmount {
		return entry.unmount()
	}
	return nil
}

func (entry *scopedReadMount) unmount() error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if !entry.mounted {
		return nil
	}
	entry.mounted = false
	return entry.closer()
}

// close unmounts the scope's mounts. A mount still in use is unmounted when
// its last reader finishes.
func (scope *readMountScope) close() error {
	scope.mu.Lock()
	scope.closed = true
	var idle []*scopedReadMount
	for _, entry := range scope.mounts {
		if entry.users == 0 {
			idle = append(idle, entry)
		}
	}
	scope.mu.Unlock()
	var errs []error
	for _, entry := range idle {
		errs = append(errs, entry.unmount())
	}
	return errors.Join(errs...)
}
