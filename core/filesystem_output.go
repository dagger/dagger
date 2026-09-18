package core

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// filesystemOutput guards typed publication. The body latch is retained after
// acquisition replaces Lazy so direct callers are excluded until their body returns.
// OutputRev is process-local and is read only through PersistedOutputRevision.
type filesystemOutput struct {
	partHost        atomic.Pointer[dagql.PartHost]
	outputMu        sync.Mutex
	acquiredOpen    sync.Mutex
	OutputRev       dagql.OutputRevision
	persistenceBody *LazyState
}

// lazyEvalBody is a value's unevaluated lazy body as seen by lazyEvalFunc.
type lazyEvalBody struct {
	evaluated func() bool
	evaluate  func(context.Context) error
}

// lazyEvalFunc is the LazyEvalFunc shared by Directory and File. kind names
// the type in errors. lazyLocked runs under outputMu and reports whether a
// transfer is pending and the value's lazy body, nil when it has none.
func (out *filesystemOutput) lazyEvalFunc(kind string, lazyLocked func() (pending bool, body *lazyEvalBody)) dagql.LazyEvalFunc {
	if host := out.partHost.Load(); host != nil && host.Managed() {
		return func(ctx context.Context) error { return host.Evaluate(ctx, "snapshot") }
	}
	out.outputMu.Lock()
	pending, body := lazyLocked()
	out.outputMu.Unlock()
	if pending {
		return func(ctx context.Context) error {
			if host := out.partHost.Load(); host != nil {
				return host.Evaluate(ctx, "snapshot")
			}
			return fmt.Errorf("%w: %s.snapshot", dagql.ErrUnavailablePart, kind)
		}
	}
	if body == nil || body.evaluated() {
		return nil
	}
	return func(ctx context.Context) error {
		if host := out.partHost.Load(); host != nil {
			return host.RunNative(ctx, dagql.LazyGroupWhole, []dagql.PartKey{"snapshot"}, body.evaluate)
		}
		return body.evaluate(ctx)
	}
}

// evaluateLazy validates output and reports completion under the winning body
// latch. Repeated or concurrent calls do not advance the output revision.
// published runs under outputMu and reports an unset accessor as an error.
func (out *filesystemOutput) evaluateLazy(ctx context.Context, state *LazyState, name string, run func(context.Context) error, published func() error) error {
	return state.Evaluate(ctx, name, func(ctx context.Context) error {
		if run != nil {
			if err := run(ctx); err != nil {
				return err
			}
		}
		out.outputMu.Lock()
		defer out.outputMu.Unlock()
		if err := published(); err != nil {
			return err
		}
		out.persistenceBody = state
		out.OutputRev++
		return nil
	})
}

func (out *filesystemOutput) rememberBodyLocked(lazy any) {
	if provider, ok := lazy.(interface{ ContainerLazyState() *LazyState }); ok {
		out.persistenceBody = provider.ContainerLazyState()
	}
}

// tryPersistenceLocked never waits for a body while holding a cache lock or
// outputMu: bodies may need both. The caller owns outputMu on entry.
func (out *filesystemOutput) tryPersistenceLocked(lazy any) (func(), error) {
	out.rememberBodyLocked(lazy)
	state := out.persistenceBody
	if lazy != nil && (state == nil || state.LazyMu == nil) {
		out.outputMu.Unlock()
		return nil, fmt.Errorf("encode filesystem output: missing lazy state for %T", lazy)
	}
	lockBody := state != nil && state.LazyMu != nil && !state.IsEvaluated()
	if lockBody {
		if !state.LazyMu.TryLock() {
			out.outputMu.Unlock()
			return nil, fmt.Errorf("%w: filesystem body in use", dagql.ErrPersistStateNotReady)
		}
	}
	return func() {
		if lockBody {
			state.LazyMu.Unlock()
		}
		out.outputMu.Unlock()
	}, nil
}

// Only ownership reads outside graph locks may wait. A body can publish through
// outputMu, so release it before waiting on that body's latch and read anew.
func (out *filesystemOutput) lockSnapshotOwnerRead(lazy func() any) (func(), error) {
	for {
		out.outputMu.Lock()
		op := lazy()
		out.rememberBodyLocked(op)
		state := out.persistenceBody
		if op != nil && (state == nil || state.LazyMu == nil) {
			out.outputMu.Unlock()
			return nil, fmt.Errorf("filesystem ownership read: missing lazy state for %T", op)
		}
		if state == nil || state.LazyMu == nil || state.IsEvaluated() {
			return out.outputMu.Unlock, nil
		}
		if state.LazyMu.TryLock() {
			return func() { state.LazyMu.Unlock(); out.outputMu.Unlock() }, nil
		}
		out.outputMu.Unlock()
		state.awaitUnlocked()
	}
}

func (file *File) ReadSnapshotOwner() (dagql.OutputRevision, []dagql.PersistedSnapshotRefLink, error) {
	unlock, err := file.lockSnapshotOwnerRead(func() any { return file.Lazy })
	if err != nil {
		return 0, nil, err
	}
	defer unlock()
	var links []dagql.PersistedSnapshotRefLink
	if id, ok := file.snapshotIdentityLocked(); ok {
		links = []dagql.PersistedSnapshotRefLink{{RefKey: id, Role: "snapshot"}}
	}
	return file.OutputRev, links, nil
}

func (dir *Directory) ReadSnapshotOwner() (dagql.OutputRevision, []dagql.PersistedSnapshotRefLink, error) {
	unlock, err := dir.lockSnapshotOwnerRead(func() any { return dir.Lazy })
	if err != nil {
		return 0, nil, err
	}
	defer unlock()
	var links []dagql.PersistedSnapshotRefLink
	if id, ok := dir.snapshotIdentityLocked(); ok {
		links = []dagql.PersistedSnapshotRefLink{{RefKey: id, Role: "snapshot"}}
	}
	return dir.OutputRev, links, nil
}

func (file *File) lockForPersistence() (func(), error) {
	if !file.outputMu.TryLock() {
		return nil, fmt.Errorf("%w: File publication in use", dagql.ErrPersistStateNotReady)
	}
	return file.tryPersistenceLocked(file.Lazy)
}

func (file *File) PersistedOutputRevision() (dagql.OutputRevision, error) {
	unlock, err := file.lockForPersistence()
	if err != nil {
		return 0, err
	}
	defer unlock()
	return file.OutputRev, nil
}

// SetPath publishes the path under the same guard used by persistence.
func (file *File) SetPath(value string) {
	file.outputMu.Lock()
	defer file.outputMu.Unlock()
	file.rememberBodyLocked(file.Lazy)
	file.File.setValue(value)
	file.OutputRev++
}

// SetSnapshot publishes an already owned reference; it performs no storage work.
func (file *File) SetSnapshot(value bkcache.ImmutableRef) {
	file.outputMu.Lock()
	defer file.outputMu.Unlock()
	file.rememberBodyLocked(file.Lazy)
	file.Snapshot.setValue(value)
	file.OutputRev++
}

// evaluateLazy validates output and reports completion under the winning body
// latch. Repeated or concurrent calls do not advance the output revision.
func (file *File) evaluateLazy(ctx context.Context, state *LazyState, name string, run func(context.Context) error) error {
	return file.filesystemOutput.evaluateLazy(ctx, state, name, run, func() error {
		if file.File == nil || file.Snapshot == nil {
			return fmt.Errorf("evaluate %s: missing File accessors", name)
		}
		if _, ok := file.File.Peek(); !ok {
			return fmt.Errorf("evaluate %s: File path is unset", name)
		}
		if _, ok := file.Snapshot.Peek(); !ok {
			return fmt.Errorf("evaluate %s: File snapshot is unset", name)
		}
		return nil
	})
}

func (dir *Directory) lockForPersistence() (func(), error) {
	if !dir.outputMu.TryLock() {
		return nil, fmt.Errorf("%w: Directory publication in use", dagql.ErrPersistStateNotReady)
	}
	return dir.tryPersistenceLocked(dir.Lazy)
}

func (dir *Directory) PersistedOutputRevision() (dagql.OutputRevision, error) {
	unlock, err := dir.lockForPersistence()
	if err != nil {
		return 0, err
	}
	defer unlock()
	return dir.OutputRev, nil
}

// SetPath publishes the path under the same guard used by persistence.
func (dir *Directory) SetPath(value string) {
	dir.outputMu.Lock()
	defer dir.outputMu.Unlock()
	dir.rememberBodyLocked(dir.Lazy)
	dir.Dir.setValue(value)
	dir.OutputRev++
}

// SetSnapshot publishes an already owned reference; it performs no storage work.
func (dir *Directory) SetSnapshot(value bkcache.ImmutableRef) {
	dir.outputMu.Lock()
	defer dir.outputMu.Unlock()
	dir.rememberBodyLocked(dir.Lazy)
	dir.Snapshot.setValue(value)
	dir.OutputRev++
}

// evaluateLazy validates output and reports completion under the winning body
// latch. Repeated or concurrent calls do not advance the output revision.
func (dir *Directory) evaluateLazy(ctx context.Context, state *LazyState, name string, run func(context.Context) error) error {
	return dir.filesystemOutput.evaluateLazy(ctx, state, name, run, func() error {
		if dir.Dir == nil || dir.Snapshot == nil {
			return fmt.Errorf("evaluate %s: missing Directory accessors", name)
		}
		if _, ok := dir.Dir.Peek(); !ok {
			return fmt.Errorf("evaluate %s: Directory path is unset", name)
		}
		if _, ok := dir.Snapshot.Peek(); !ok {
			return fmt.Errorf("evaluate %s: Directory snapshot is unset", name)
		}
		return nil
	})
}

func (out *filesystemOutput) BindPartHost(host *dagql.PartHost) {
	out.partHost.CompareAndSwap(nil, host)
}

func (out *filesystemOutput) PartHostBinding() *dagql.PartHost { return out.partHost.Load() }

func (file *File) PersistedSnapshotRefLinksChecked() ([]dagql.PersistedSnapshotRefLink, error) {
	unlock, err := file.lockForPersistence()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if id, ok := file.snapshotIdentityLocked(); ok {
		return []dagql.PersistedSnapshotRefLink{{RefKey: id, Role: "snapshot"}}, nil
	}
	return nil, nil
}
func (dir *Directory) PersistedSnapshotRefLinksChecked() ([]dagql.PersistedSnapshotRefLink, error) {
	unlock, err := dir.lockForPersistence()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if id, ok := dir.snapshotIdentityLocked(); ok {
		return []dagql.PersistedSnapshotRefLink{{RefKey: id, Role: "snapshot"}}, nil
	}
	return nil, nil
}
