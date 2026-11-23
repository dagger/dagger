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
	// observe, when set, sees each holder an acquisition marks, and the
	// kind of work it named (ObserveMarks).
	observe atomic.Pointer[func(holder uint64, kind LockHolder)]
	// kind is the kind of work its writer named (WriteLockAs), 0 when it
	// named none. Only the writer that holds m reads or writes it: it is
	// set after the writer takes m, and read and cleared as it releases m.
	kind LockHolder
	// waiting counts the acquisitions waiting for m now, however they took
	// it: through waitFor, or Lock or RLock once a try to take m at once
	// failed (Waiting). A writer records its release (released) only while
	// one waits, so a release that keeps no one waiting adds a plain load of
	// it and no write.
	waiting atomic.Int32
	// released counts the releases of m by writers that someone waited
	// for, in its bits above the low 8, and holds in the low 8 the kind of
	// the last of them. Only the writer that holds m writes it: a waiter
	// that reads it before and after its wait tells from the count whether
	// a writer let it in, and from the kind which.
	released atomic.Uint64
	// holds, when set, sees each hold of m for writing as it ends
	// (ObserveHolds). heldAt is when the writer that holds m took it, by
	// holdClock, and waited how long it waited to: set as it takes m while
	// holds is set, and 0 otherwise, so that a release with no observer
	// reads one field of its own. Only the writer that holds m reads or
	// writes them.
	holds  atomic.Pointer[func(kind LockHolder, waited, held time.Duration)]
	heldAt int64
	waited time.Duration
}

// LockHolder names the kind of work holding an RWMutex for writing, as
// WriteLockAs gives it, so that a waiter can tell what it waited behind
// (LockWait). A package registers its kinds with NewLockHolder. The zero
// LockHolder is a writer that named no kind.
type LockHolder uint8

// LockHolders bounds the LockHolder values, LockHolderReaders included, so
// that a counter by kind is a small fixed array: every LockHolder is below
// it.
const LockHolders = 32

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
// kind of the writer whose release let it in.
//
// A reader waits only for a writer: the one that held the lock, or the one
// waiting for it that the reader queued behind, whose release lets it in.
// A writer can wait for readers too, and one that no writer's release let
// in is LockHolderReaders. A writer records its release only while someone
// waits, so a wait that began as a writer was releasing the lock, in the
// few instructions between its look at the waiting count and its unlock,
// may see no release: a reader then names no kind ("other", never
// "readers"), and a writer LockHolderReaders. When writers took turns ahead
// of a waiting writer, the last of them is named. The zero LockWait is an
// acquisition that didn't wait.
type LockWait struct {
	Waited time.Duration
	Holder LockHolder
}

// Lock locks m for writing, its holder unknown. While it has to wait, it
// counts itself as waiting (Waiting).
func (m *RWMutex) Lock() {
	m.took(m.lock())
}

// LockSelfTimed locks m for writing, as Lock does, for a holder that times
// its own hold: m's hold observer (ObserveHolds) doesn't see it.
func (m *RWMutex) LockSelfTimed() {
	m.lock()
}

// lock locks m for writing, its holder unknown, and returns how long it
// waited.
func (m *RWMutex) lock() time.Duration {
	var waited time.Duration
	if !m.RWMutex.TryLock() {
		m.waiting.Add(1)
		start := time.Now()
		m.RWMutex.Lock()
		waited = time.Since(start)
		m.waiting.Add(-1)
	}
	m.mark(HolderUnknown)
	return waited
}

// TryLock tries to lock m for writing, its holder unknown.
func (m *RWMutex) TryLock() bool {
	if !m.RWMutex.TryLock() {
		return false
	}
	m.mark(HolderUnknown)
	m.took(0)
	return true
}

// ObserveHolds calls f with each hold of m for writing as it ends, until
// the returned stop is called: the kind of work its writer named
// (WriteLockAs), 0 for none, how long the writer waited to take m, and how
// long it held m. A hold taken before f was set, or ended after stop, isn't
// seen, nor is one taken with LockSelfTimed. f runs in the releasing
// goroutine while m is still held, so it must not take m, and should cost
// little: it delays every waiter. One f at a time: a later call replaces an
// earlier one.
func (m *RWMutex) ObserveHolds(f func(kind LockHolder, waited, held time.Duration)) (stop func()) {
	m.holds.Store(&f)
	return func() { m.holds.CompareAndSwap(&f, nil) }
}

// holdClock is the start of the clock holds are timed by (heldAt).
var holdClock = time.Now()

// took notes, for m's hold observer if it has one, when the writer that
// now holds m took it and how long it waited to (heldAt).
func (m *RWMutex) took(waited time.Duration) {
	if m.holds.Load() == nil {
		return
	}
	m.heldAt = int64(time.Since(holdClock)) + 1
	m.waited = waited
}

// endHold tells m's hold observer, if it still has one, of the hold its
// writer is ending (took).
func (m *RWMutex) endHold() {
	held := time.Duration(int64(time.Since(holdClock)) + 1 - m.heldAt)
	m.heldAt = 0
	if f := m.holds.Load(); f != nil {
		(*f)(m.kind, m.waited, held)
	}
}

// Unlock unlocks m for writing. While someone waits for m, it records for
// them that a writer of its kind released m (released), and it tells m's
// hold observer of the hold (ObserveHolds). It clears the kind for the
// next writer.
func (m *RWMutex) Unlock() {
	if m.waiting.Load() != 0 {
		m.recordRelease()
	}
	if m.heldAt != 0 {
		m.endHold()
	}
	m.kind = 0
	m.release()
	m.RWMutex.Unlock()
}

// recordRelease records, for a waiter, that a writer of m's kind released m.
// The writer that holds m calls it as it unlocks.
func (m *RWMutex) recordRelease() {
	m.released.Store((m.released.Load()>>8+1)<<8 | uint64(m.kind))
}

// waitFor takes m, for reading when read is set and for writing otherwise,
// once a try to take it at once failed, and returns how long it waited and
// the kind of the writer that let it in. While work under ctx is recorded,
// the wait is a lock wait of ctx's op on the resource ident, naming m's
// holder when the wait began and that kind. A writer marks itself after.
func (m *RWMutex) waitFor(ctx context.Context, ident string, read bool) LockWait {
	w := beginLockWait(ctx, m, ident)
	m.waiting.Add(1)
	before := m.released.Load()
	start := time.Now()
	if read {
		m.RWMutex.RLock()
		m.markReaders()
	} else {
		m.RWMutex.Lock()
	}
	lw := LockWait{Waited: time.Since(start)}
	after := m.released.Load()
	m.waiting.Add(-1)
	switch {
	case after>>8 != before>>8:
		lw.Holder = LockHolder(after & 0xff)
	case read:
		// A writer kept the reader out, as it released the lock: which
		// one isn't known (LockWait).
		lw.Holder = 0
	default:
		lw.Holder = LockHolderReaders
	}
	if w != nil {
		w.holderKind = w.r.Intern(lw.Holder.String())
	}
	w.End()
	return lw
}

// RLock locks m for reading. While it has to wait, it counts itself as
// waiting (Waiting).
func (m *RWMutex) RLock() {
	if !m.RWMutex.TryRLock() {
		m.waiting.Add(1)
		m.RWMutex.RLock()
		m.waiting.Add(-1)
	}
	m.markReaders()
}

// Waiting reports whether any acquisition of m is waiting for it now, for
// reading or for writing, however it takes m. A holder of m for writing
// asks it between two of its writes, to let m go only when someone waits
// (dagql's RemoteWrites).
func (m *RWMutex) Waiting() bool {
	return m.waiting.Load() != 0
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
// exclusive acquisition of m marks while a recorder is active, and the kind
// of work it named (WriteLockAs), until the returned stop is called; for
// tests, which can tell from f's caller which code took m and whether it
// named its op and its kind.
func (m *RWMutex) ObserveMarks(f func(holder uint64, kind LockHolder)) (stop func()) {
	m.observe.Store(&f)
	return func() { m.observe.Store(nil) }
}

func (m *RWMutex) observed(holder uint64) {
	if f := m.observe.Load(); f != nil {
		(*f)(holder, m.kind)
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
