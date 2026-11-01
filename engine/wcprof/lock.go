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
// HolderUnknown, never the holder that released it; and a waiter whose
// read overlaps a change of holder reads HolderUnknown too.
type RWMutex struct {
	sync.RWMutex
	holder atomic.Uint64
	since  atomic.Int64
	// seq counts changes of holder: odd while one is written (holderAt).
	seq atomic.Uint64
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
		m.set(HolderUnknown, 0)
	}
	m.RWMutex.RUnlock()
}

// mark records holder as m's holder from now, while a recorder is active.
func (m *RWMutex) mark(holder uint64) {
	r := global.Load()
	if r == nil {
		return
	}
	m.set(holder, r.Now())
}

// set changes m's holder and its time, seq odd while it does (holderAt).
// Only one holder sets them at a time, but readers: they set only
// HolderReaders or HolderUnknown with no time, so two at once can't mix
// another holder's time in.
func (m *RWMutex) set(holder uint64, since int64) {
	m.seq.Add(1)
	m.holder.Store(holder)
	m.since.Store(since)
	m.seq.Add(1)
}

// markReaders records m as held for reading, writing only when its holder
// changes, so that readers don't contend on it. Readers have no time: one
// reader's would count the idle time before a later one as held.
func (m *RWMutex) markReaders() {
	if global.Load() == nil || m.holder.Load() == HolderReaders {
		return
	}
	m.set(HolderReaders, 0)
}

// release makes m's holder unknown, before m is unlocked.
func (m *RWMutex) release() {
	if global.Load() == nil {
		return
	}
	m.set(HolderUnknown, 0)
}

// markOp records the op of ctx as m's holder, or HolderUnknown when ctx
// names none, while a recorder is active; it looks nothing up otherwise, as
// its caller holds m.
func (m *RWMutex) markOp(ctx context.Context) {
	r := global.Load()
	if r == nil {
		return
	}
	op := CurrentOpID(ctx)
	if op == 0 {
		op = HolderUnknown
	}
	m.set(op, r.Now())
}

// Holder is m's holder and when it took m, as a wait on m begun now would
// record them; 0 while no recorder has been active.
func (m *RWMutex) Holder() (uint64, int64) {
	return m.holderAt()
}

// holderAt reads m's holder and when it took m: HolderUnknown with no time
// when the holder changed while it read, even to another and back.
func (m *RWMutex) holderAt() (uint64, int64) {
	return m.holderAtWith(nil)
}

// holderAtWith is holderAt, running between, when set, after it has read
// the holder and its time; for tests.
func (m *RWMutex) holderAtWith(between func()) (uint64, int64) {
	seq := m.seq.Load()
	if seq%2 == 1 {
		return HolderUnknown, 0
	}
	h, since := m.holder.Load(), m.since.Load()
	if between != nil {
		between()
	}
	if m.seq.Load() != seq {
		return HolderUnknown, 0
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
