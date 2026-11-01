package wcprof

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
)

// Holders a recorded wait on an RWMutex can name in place of an op.
const (
	// HolderUnknown: the lock was taken directly (Lock or TryLock), with no
	// context to name the op that took it.
	HolderUnknown uint64 = math.MaxUint64
	// HolderReaders: the lock was held for reading.
	HolderReaders uint64 = math.MaxUint64 - 1
)

// RWMutex is a sync.RWMutex that remembers who holds it while a recorder
// is active, so that a wait on it recorded by WriteLock or ReadLock names
// its holder: the op that took it through WriteLock, HolderUnknown when it
// was taken directly, or HolderReaders when it was held for reading. Every
// acquisition marks the holder, so the name is never a stale one.
type RWMutex struct {
	sync.RWMutex
	holder atomic.Uint64
	since  atomic.Int64
}

// Lock locks m for writing, its holder unknown.
func (m *RWMutex) Lock() {
	m.RWMutex.Lock()
	m.mark(HolderUnknown)
}

// TryLock tries to lock m for writing, its holder unknown.
func (m *RWMutex) TryLock() bool {
	if !m.RWMutex.TryLock() {
		return false
	}
	m.mark(HolderUnknown)
	return true
}

// RLock locks m for reading.
func (m *RWMutex) RLock() {
	m.RWMutex.RLock()
	m.markReaders()
}

// TryRLock tries to lock m for reading.
func (m *RWMutex) TryRLock() bool {
	if !m.RWMutex.TryRLock() {
		return false
	}
	m.markReaders()
	return true
}

// mark records holder as m's holder from now, while a recorder is active.
func (m *RWMutex) mark(holder uint64) {
	r := global.Load()
	if r == nil {
		return
	}
	m.holder.Store(holder)
	m.since.Store(r.Now())
}

// markReaders records m as held for reading, writing only when its holder
// changes, so that readers don't contend on it.
func (m *RWMutex) markReaders() {
	if global.Load() == nil || m.holder.Load() == HolderReaders {
		return
	}
	m.mark(HolderReaders)
}

// markOp records the op of ctx as m's holder, or HolderUnknown when ctx
// names none.
func (m *RWMutex) markOp(ctx context.Context) {
	op := CurrentOpID(ctx)
	if op == 0 {
		op = HolderUnknown
	}
	m.mark(op)
}

// RWLocker is a read-write lock: a sync.RWMutex, or an RWMutex.
type RWLocker interface {
	Lock()
	TryLock() bool
	RLock()
	TryRLock() bool
}
