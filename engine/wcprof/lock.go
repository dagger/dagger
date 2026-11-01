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
// was taken directly, or HolderReaders when it was held for reading.
// Releasing it makes the holder unknown before the lock is free, so a
// waiter that reads it before the next taker marks itself reads
// HolderUnknown, never the holder that released it.
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

// Unlock unlocks m for writing.
func (m *RWMutex) Unlock() {
	m.release()
	m.RWMutex.Unlock()
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

// RUnlock unlocks m for reading. A reader leaving while others still read
// makes the holder unknown until the next reader marks it: unknown, not
// wrong.
func (m *RWMutex) RUnlock() {
	if global.Load() != nil && m.holder.Load() == HolderReaders {
		m.holder.Store(HolderUnknown)
	}
	m.RWMutex.RUnlock()
}

// mark records holder as m's holder from now, while a recorder is active:
// HolderUnknown first, then the time, then the holder, so that a reader
// that sees the same holder before and after reading the time has that
// holder's time (holderAt).
func (m *RWMutex) mark(holder uint64) {
	r := global.Load()
	if r == nil {
		return
	}
	m.holder.Store(HolderUnknown)
	m.since.Store(r.Now())
	m.holder.Store(holder)
}

// markReaders records m as held for reading, writing only when its holder
// changes, so that readers don't contend on it. Readers have no time: one
// reader's would count the idle time before a later one as held.
func (m *RWMutex) markReaders() {
	if global.Load() == nil || m.holder.Load() == HolderReaders {
		return
	}
	m.holder.Store(HolderUnknown)
	m.since.Store(0)
	m.holder.Store(HolderReaders)
}

// release makes m's holder unknown, before m is unlocked.
func (m *RWMutex) release() {
	if global.Load() == nil {
		return
	}
	m.holder.Store(HolderUnknown)
	m.since.Store(0)
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

// holderAt reads m's holder and when it took m: HolderUnknown with no time
// when the holder changed while it read.
func (m *RWMutex) holderAt() (uint64, int64) {
	h := m.holder.Load()
	since := m.since.Load()
	if m.holder.Load() != h {
		return HolderUnknown, 0
	}
	if h == HolderReaders {
		since = 0
	}
	return h, since
}

// RWLocker is a read-write lock: a sync.RWMutex, or an RWMutex.
type RWLocker interface {
	Lock()
	TryLock() bool
	RLock()
	TryRLock() bool
}
