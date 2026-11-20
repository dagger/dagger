package wcprof

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"
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
// read overlaps a change of holder reads HolderUnknown too. Readers are
// counted while a recorder is active, so the lock stays held by
// HolderReaders until the last of them leaves.
//
// Whether or not anything records, a writer can name the kind of work it
// is (WriteLockAs, LockHolder), and a waited acquisition (ReadLockWaited,
// WriteLockAs) returns how long it waited and the kind of the writer that
// let it in (LockWait).
type RWMutex struct {
	sync.RWMutex
	holder atomic.Uint64
	since  atomic.Int64
	// seq counts changes of holder: odd while one is written (holderAt).
	seq atomic.Uint64
	// readers counts the readers holding m that took it while a recorder
	// was active: each counts itself once it holds m, and uncounts itself
	// before it releases m, so a positive count means a counted reader
	// holds m and no writer can. A reader that took m before any recorder
	// was active isn't counted, and its leaving can make the holder
	// unknown while counted readers still hold m: unknown, never wrong.
	readers atomic.Int64
	// observe, when set, sees each holder an acquisition marks (ObserveMarks).
	observe atomic.Pointer[func(holder uint64)]
	// kind is the kind of work its writer named (WriteLockAs), 0 when it
	// named none. Only the writer that holds m reads or writes it: it is
	// set after the writer takes m, and read and cleared as it releases m.
	kind LockHolder
	// released counts the releases of m by writers, in its bits above the
	// low 8, and holds in the low 8 the kind of the last writer to release
	// it. Only the writer that holds m writes it, with a plain load and
	// store: a waiter that reads it before and after its wait tells from
	// the count whether a writer let it in, and from the kind which.
	released atomic.Uint64
}

// LockHolder names the kind of work holding an RWMutex for writing, as
// WriteLockAs gives it, so that a waiter can tell what it waited behind
// (LockWait). A package registers its kinds with NewLockHolder. The zero
// LockHolder is a writer that named no kind.
type LockHolder uint8

// LockHolders bounds the LockHolder values, LockHolderReaders included, so
// that a counter by kind is a small fixed array: every LockHolder is below
// it.
const LockHolders = 16

// LockHolderReaders is the holder of a waited acquisition that no writer
// let in: it waited only for readers to leave.
const LockHolderReaders LockHolder = LockHolders - 1

// lockHolderNames are the registered kinds' names, by LockHolder. They're
// written only while packages initialize, before any lock is taken.
var (
	lockHolderNames     = [LockHolders]string{0: "other", LockHolderReaders: "readers"}
	lockHolderNameCount = 1
)

// NewLockHolder registers a kind of work that holds an RWMutex for
// writing, named name. Call it from a package-level var, so it runs while
// packages initialize: there are at most LockHolders-2.
func NewLockHolder(name string) LockHolder {
	if lockHolderNameCount == int(LockHolderReaders) {
		panic("wcprof: too many lock holder kinds")
	}
	h := LockHolder(lockHolderNameCount)
	lockHolderNames[h] = name
	lockHolderNameCount++
	return h
}

// String is the kind's name: its registered name, "other" for a writer
// that named none, or "readers".
func (h LockHolder) String() string {
	if int(h) < len(lockHolderNames) && lockHolderNames[h] != "" {
		return lockHolderNames[h]
	}
	return "other"
}

// LockWait is an acquisition of an RWMutex that had to wait, as
// ReadLockWaited and WriteLockAs return it: how long it waited, and the
// kind of the writer whose release let it in, or LockHolderReaders when no
// writer released the lock while it waited. A reader that waits is always
// let in by a writer: the one that held the lock, or the one waiting for
// it that the reader queued behind. A writer that waits can be let in by
// the last reader to leave. When writers took turns ahead of a waiting
// writer, the last of them is named. The zero LockWait is an acquisition
// that didn't wait.
type LockWait struct {
	Waited time.Duration
	Holder LockHolder
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

// Unlock unlocks m for writing. It records, for a waiter, that a writer of
// its kind released m (released), and clears the kind for the next writer.
func (m *RWMutex) Unlock() {
	kind := m.kind
	m.kind = 0
	m.released.Store((m.released.Load()>>8+1)<<8 | uint64(kind))
	m.release()
	m.RWMutex.Unlock()
}

// waitFor takes m, for reading when read is set and for writing otherwise,
// once a try to take it at once failed, and returns how long it waited and
// the kind of the writer that let it in. While work under ctx is recorded,
// the wait is a lock wait of ctx's op on the resource ident, naming m's
// holder when the wait began and that kind. A writer marks itself after.
func (m *RWMutex) waitFor(ctx context.Context, ident string, read bool) LockWait {
	w := beginLockWait(ctx, m, ident)
	before := m.released.Load()
	start := time.Now()
	if read {
		m.RLock()
	} else {
		m.RWMutex.Lock()
	}
	lw := LockWait{Waited: time.Since(start), Holder: LockHolderReaders}
	if after := m.released.Load(); after>>8 != before>>8 {
		lw.Holder = LockHolder(after & 0xff)
	}
	if w != nil {
		w.holderKind = w.r.Intern(lw.Holder.String())
	}
	w.End()
	return lw
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

// RUnlock unlocks m for reading. The last reader leaving makes the holder
// unknown.
func (m *RWMutex) RUnlock() {
	if global.Load() != nil {
		m.readerLeaving()
	}
	m.RWMutex.RUnlock()
}

// readerLeaving uncounts a reader about to release m, and makes the holder
// unknown when it was the last. A count below zero, from a reader that took
// m before any recorder was active, starts again from zero. A reader that
// counts itself just as the last one leaves, seeing the holder still the
// readers, holds m as unknown until the next reader marks it: unknown,
// never wrong.
func (m *RWMutex) readerLeaving() {
	n := m.readers.Add(-1)
	if n > 0 {
		return
	}
	if n < 0 {
		m.readers.CompareAndSwap(n, 0)
	}
	if m.holder.Load() == HolderReaders {
		m.set(HolderUnknown, 0)
	}
}

// mark records holder as m's holder from now, while a recorder is active.
func (m *RWMutex) mark(holder uint64) {
	r := global.Load()
	if r == nil {
		return
	}
	m.set(holder, r.Now())
	m.observed(holder)
}

// ObserveMarks calls f, in the acquiring goroutine, with the holder each
// exclusive acquisition of m marks while a recorder is active, until the
// returned stop is called; for tests, which can tell from f's caller which
// code took m and whether it named its op.
func (m *RWMutex) ObserveMarks(f func(holder uint64)) (stop func()) {
	m.observe.Store(&f)
	return func() { m.observe.Store(nil) }
}

func (m *RWMutex) observed(holder uint64) {
	if f := m.observe.Load(); f != nil {
		(*f)(holder)
	}
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

// markReaders counts a reader that holds m, and records m as held for
// reading, writing the holder only when it changes, so that readers don't
// contend on it. Readers have no time: one reader's would count the idle
// time before a later one as held.
func (m *RWMutex) markReaders() {
	if global.Load() == nil {
		return
	}
	m.readers.Add(1)
	if m.holder.Load() == HolderReaders {
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
	m.observed(op)
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
