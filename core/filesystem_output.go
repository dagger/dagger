package core

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// filesystemOutput guards typed publication. The body latch is retained after
// Lazy is cleared so direct callers are excluded until their body returns.
// OutputRev is process-local and is read only through PersistedOutputRevision.
type filesystemOutput struct {
	partHost        atomic.Pointer[dagql.PartHost]
	outputMu        sync.Mutex
	acquiredOpen    sync.Mutex
	OutputRev       dagql.OutputRevision
	persistenceBody *LazyState
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
	if state != nil && state.LazyMu != nil {
		if !state.LazyMu.TryLock() {
			out.outputMu.Unlock()
			return nil, fmt.Errorf("%w: filesystem body in use", dagql.ErrPersistStateNotReady)
		}
	}
	return func() {
		if state != nil && state.LazyMu != nil {
			state.LazyMu.Unlock()
		}
		out.outputMu.Unlock()
	}, nil
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

func (file *File) finishLazyLocked(lazy Lazy[*File]) {
	if lazy == nil {
		return
	}
	file.rememberBodyLocked(lazy)
	if _, restored := lazy.(*FileRestoreLazy); !restored {
		file.completedRecipe = lazy
	}
	if file.Lazy == lazy {
		file.Lazy = nil
	}
	file.OutputRev++
}

func (file *File) clearLazy() {
	file.outputMu.Lock()
	defer file.outputMu.Unlock()
	file.finishLazyLocked(file.Lazy)
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

func (dir *Directory) finishLazyLocked(lazy Lazy[*Directory]) {
	if lazy == nil {
		return
	}
	dir.rememberBodyLocked(lazy)
	if _, restored := lazy.(*DirectoryRestoreLazy); !restored {
		dir.completedRecipe = lazy
	}
	if dir.Lazy == lazy {
		dir.Lazy = nil
	}
	dir.OutputRev++
}

func (dir *Directory) clearLazy() {
	dir.outputMu.Lock()
	defer dir.outputMu.Unlock()
	dir.finishLazyLocked(dir.Lazy)
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
