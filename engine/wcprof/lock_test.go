package wcprof

import (
	"bytes"
	"context"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// lockWait has one goroutine take mu with WriteLock under ctx while the
// test holds it, and returns the wait it recorded. The test holds mu on
// entry; lockWait releases it only once the goroutine is in mu's own Lock,
// after its wait has read mu's holder, so the release can't change the
// holder the wait reads.
func lockWait(t *testing.T, r *Recorder, ctx context.Context, mu *RWMutex, unlock func()) Event {
	t.Helper()
	locked := make(chan struct{})
	go writeLockWaiter(ctx, mu, locked)
	for !waiterInLock() {
		runtime.Gosched()
	}
	unlock()
	<-locked
	mu.Unlock()
	var waits []Event
	for _, ev := range drainEvents(r) {
		if ev.Type == EventTypeWait {
			waits = append(waits, ev)
		}
	}
	if len(waits) != 1 {
		t.Fatalf("expected 1 wait, got %+v", waits)
	}
	return waits[0]
}

// writeLockWaiter takes mu with WriteLock under ctx, then closes locked.
func writeLockWaiter(ctx context.Context, mu *RWMutex, locked chan<- struct{}) {
	WriteLock(ctx, mu, "test.mu")
	close(locked)
}

// waiterInLock reports whether writeLockWaiter is in the sync.RWMutex Lock
// that WriteLock has its wait call itself, after the wait has read the
// holder; not in another Lock before it, such as the recorder's as the wait
// interns its ident.
func waiterInLock() bool {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	for _, g := range bytes.Split(buf, []byte("\n\n")) {
		if bytes.Contains(g, []byte("wcprof.writeLockWaiter(")) && lockedByWriteLock.Match(g) {
			return true
		}
	}
	return false
}

// lockedByWriteLock matches a goroutine's stack whose sync.RWMutex Lock frame
// is called from an RWMutex's wait (waitFor): each frame is a line naming the
// function and a line naming its file.
var lockedByWriteLock = regexp.MustCompile(`(?m)^sync\.\(\*RWMutex\)\.Lock\(.*\n.*\n\S*/engine/wcprof\.\(\*RWMutex\)\.waitFor\(`)

// A wait on an RWMutex names the op that held it, taken with WriteLock, and
// when it took it; the dump writes them as the wait's holder.
func TestWriteLockNamesItsHolder(t *testing.T) {
	r := withTestRecorder(t, 0)
	holderCtx, holder := BeginOp(context.Background(), OpKindRemoteCache, "remotecache.merge.commit", OpOpts{})
	waiterCtx, _ := BeginOp(context.Background(), OpKindLazy, "Container.withExec", OpOpts{})
	var mu RWMutex
	WriteLock(holderCtx, &mu, "test.mu")
	took := r.Now()
	w := lockWait(t, r, waiterCtx, &mu, mu.Unlock)
	if w.ResultID != holder.ID() || w.Bytes <= 0 || w.Bytes > took || w.Bytes > w.StartNS {
		t.Fatalf("wait %+v: want holder %d, taken at or before %d", w, holder.ID(), took)
	}
	d := toDumpEvent(w)
	if d.HolderID != holder.ID() || d.HolderSinceNS != w.Bytes || d.ResultID != 0 || d.Bytes != 0 {
		t.Fatalf("dump %+v: want the holder as h and hs, not as r and b", d)
	}
}

// A lock taken directly, with no context to name its taker, is held by
// HolderUnknown: never by the op that took it before through WriteLock.
func TestDirectLockHolderIsUnknown(t *testing.T) {
	r := withTestRecorder(t, 0)
	earlierCtx, _ := BeginOp(context.Background(), OpKindRemoteCache, "remotecache.merge.commit", OpOpts{})
	waiterCtx, _ := BeginOp(context.Background(), OpKindLazy, "Container.withExec", OpOpts{})
	var mu RWMutex
	WriteLock(earlierCtx, &mu, "test.mu")
	mu.Unlock()
	mu.Lock()
	if w := lockWait(t, r, waiterCtx, &mu, mu.Unlock); w.ResultID != HolderUnknown {
		t.Fatalf("wait %+v: want holder HolderUnknown, not the earlier holder", w)
	}
}

// A lock held for reading is held by HolderReaders.
func TestReadLockedHolderIsReaders(t *testing.T) {
	r := withTestRecorder(t, 0)
	waiterCtx, _ := BeginOp(context.Background(), OpKindLazy, "Container.withExec", OpOpts{})
	var mu RWMutex
	mu.RLock()
	if w := lockWait(t, r, waiterCtx, &mu, mu.RUnlock); w.ResultID != HolderReaders {
		t.Fatalf("wait %+v: want holder HolderReaders", w)
	}
}

// A lock that overlapping readers hold stays held by HolderReaders when one
// of them leaves: a writer that starts waiting while the others still read
// names the readers, not an unknown holder.
func TestOverlappingReadersHolderIsReaders(t *testing.T) {
	r := withTestRecorder(t, 0)
	waiterCtx, _ := BeginOp(context.Background(), OpKindLazy, "Container.withExec", OpOpts{})
	var mu RWMutex
	mu.RLock()
	mu.RLock()
	mu.RUnlock() //nolint:staticcheck // SA2001: a reader that leaves while another still holds the lock.
	if w := lockWait(t, r, waiterCtx, &mu, mu.RUnlock); w.ResultID != HolderReaders {
		t.Fatalf("wait %+v: want holder HolderReaders while a reader still holds the lock", w)
	}
}

// Once the last reader leaves, the lock's holder is unknown until its next
// taker marks itself: a writer that takes it and hasn't marked itself yet is
// never named as the readers.
func TestLastReaderLeavingIsNotNamed(t *testing.T) {
	r := withTestRecorder(t, 0)
	waiterCtx, _ := BeginOp(context.Background(), OpKindLazy, "Container.withExec", OpOpts{})
	var mu RWMutex
	mu.RLock()
	mu.RLock()
	mu.RUnlock() //nolint:staticcheck // SA2001: a reader that leaves while another still holds the lock.
	mu.RUnlock()
	// The next taker holds the mutex but hasn't marked itself yet.
	mu.RWMutex.Lock()
	if w := lockWait(t, r, waiterCtx, &mu, mu.RWMutex.Unlock); w.ResultID != HolderUnknown {
		t.Fatalf("wait %+v: want holder HolderUnknown once the readers have left", w)
	}
}

// A lock released by its holder and taken again before the new taker has
// marked itself never names the released holder: the release itself makes
// the holder unknown.
func TestReleasedHolderIsNotNamed(t *testing.T) {
	r := withTestRecorder(t, 0)
	earlierCtx, _ := BeginOp(context.Background(), OpKindRemoteCache, "remotecache.merge.commit", OpOpts{})
	waiterCtx, _ := BeginOp(context.Background(), OpKindLazy, "Container.withExec", OpOpts{})
	var mu RWMutex
	WriteLock(earlierCtx, &mu, "test.mu")
	mu.Unlock()
	// The next taker holds the mutex but hasn't marked itself yet.
	mu.RWMutex.Lock()
	if w := lockWait(t, r, waiterCtx, &mu, mu.RWMutex.Unlock); w.ResultID != HolderUnknown {
		t.Fatalf("wait %+v: want holder HolderUnknown, not the released holder", w)
	}
}

// Readers carry no acquisition time: one reader's time would count the idle
// time before a later reader as held.
func TestReadersHaveNoAcquisitionTime(t *testing.T) {
	r := withTestRecorder(t, 0)
	waiterCtx, _ := BeginOp(context.Background(), OpKindLazy, "Container.withExec", OpOpts{})
	var mu RWMutex
	mu.RLock()
	mu.RUnlock() //nolint:staticcheck // SA2001: an earlier reader that comes and goes, whose time a later reader mustn't inherit.
	mu.RLock()
	w := lockWait(t, r, waiterCtx, &mu, mu.RUnlock)
	if w.ResultID != HolderReaders || w.Bytes != 0 {
		t.Fatalf("wait %+v: want holder HolderReaders with no acquisition time", w)
	}
}

// A holder read while the lock changes hands, even to another holder and
// back to the first, reads as unknown: never the first holder with the
// other's time.
func TestHolderReadAcrossAChangeIsUnknown(t *testing.T) {
	r := withTestRecorder(t, 0)
	var mu RWMutex
	mu.mark(7)
	h, since := mu.holderAtWith(func() {
		mu.release()
		mu.mark(8)
		mu.release()
		mu.mark(7)
	})
	if h != HolderUnknown || since != 0 {
		t.Fatalf("holder %d since %d: want HolderUnknown with no time", h, since)
	}
	if h, since := mu.holderAt(); h != 7 || since <= 0 || since > r.Now() {
		t.Fatalf("holder %d since %d: want 7, taken by now", h, since)
	}
}

// countingCtx counts the lookups of its values.
type countingCtx struct {
	context.Context
	lookups int
}

func (c *countingCtx) Value(key any) any {
	c.lookups++
	return c.Context.Value(key)
}

// With no recorder, WriteLock on an RWMutex doesn't look into its context:
// it holds the lock then, on every engine that isn't recording.
func TestWriteLockWithoutRecorderLooksNothingUp(t *testing.T) {
	prev, prevOn := global.Load(), globalOn.Load()
	global.Store(nil)
	globalOn.Store(false)
	t.Cleanup(func() {
		global.Store(prev)
		globalOn.Store(prevOn)
	})
	ctx := &countingCtx{Context: context.Background()}
	var mu RWMutex
	WriteLock(ctx, &mu, "test.mu")
	mu.Unlock()
	if ctx.lookups != 0 {
		t.Fatalf("WriteLock looked into its context %d times with no recorder", ctx.lookups)
	}
}

// testHolder is a kind of work a test's writer names itself as.
var testHolder = NewLockHolder("test.commit")

// blockedIn waits until a goroutine whose stack has a frame of fn is blocked
// on a lock: a sync.RWMutex's, or the sync.Mutex it queues its writers on;
// and fails the test after 10 s.
func blockedIn(t *testing.T, fn string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		buf := make([]byte, 1<<20)
		for _, g := range bytes.Split(buf[:runtime.Stack(buf, true)], []byte("\n\n")) {
			if bytes.Contains(g, []byte(fn)) && (bytes.Contains(g, []byte("[sync.RWMutex.")) || bytes.Contains(g, []byte("[sync.Mutex.Lock"))) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing blocked on the lock in %s", fn)
		}
		runtime.Gosched()
	}
}

// withoutRecorder runs the test with no recorder, as every engine that
// doesn't record runs.
func withoutRecorder(t *testing.T) {
	prev, prevOn := global.Load(), globalOn.Load()
	global.Store(nil)
	globalOn.Store(false)
	t.Cleanup(func() {
		global.Store(prev)
		globalOn.Store(prevOn)
	})
}

// A reader that waits for a writer is told, with no recorder, how long it
// waited and the kind of work of the writer that let it in.
func TestReadWaitNamesTheWriterThatLetItIn(t *testing.T) {
	withoutRecorder(t)
	var mu RWMutex
	WriteLockAs(context.Background(), &mu, "test.mu", testHolder)
	got := make(chan LockWait)
	go func() {
		lw := ReadLockWaited(context.Background(), &mu, "test.mu")
		mu.RUnlock()
		got <- lw
	}()
	blockedIn(t, "wcprof.ReadLockWaited(")
	time.Sleep(time.Millisecond)
	mu.Unlock()
	lw := <-got
	if lw.Holder != testHolder || lw.Waited < time.Millisecond {
		t.Fatalf("wait %+v: want at least 1ms behind %s", lw, testHolder)
	}
	if lw.Holder.String() != "test.commit" {
		t.Fatalf("holder named %q", lw.Holder.String())
	}
}

// A writer that waits only for readers is told it waited for readers; one
// that waits for a writer that named no kind is told "other".
func TestWriteWaitNamesReadersOrAnUnnamedWriter(t *testing.T) {
	withoutRecorder(t)
	for name, tc := range map[string]struct {
		hold, release func(*RWMutex)
		want          LockHolder
	}{
		"readers":          {hold: (*RWMutex).RLock, release: (*RWMutex).RUnlock, want: LockHolderReaders},
		"an unnamed write": {hold: (*RWMutex).Lock, release: (*RWMutex).Unlock, want: 0},
	} {
		t.Run(name, func(t *testing.T) {
			var mu RWMutex
			// An earlier writer of a named kind, which released mu before
			// the wait began, is not who let the waiter in.
			WriteLockAs(context.Background(), &mu, "test.mu", testHolder)
			mu.Unlock()
			tc.hold(&mu)
			got := make(chan LockWait)
			go func() {
				lw := WriteLockAs(context.Background(), &mu, "test.mu", testHolder)
				mu.Unlock()
				got <- lw
			}()
			blockedIn(t, "wcprof.WriteLockAs(")
			tc.release(&mu)
			lw := <-got
			if lw.Holder != tc.want || lw.Waited <= 0 {
				t.Fatalf("wait %+v: want behind %s", lw, tc.want)
			}
		})
	}
}

// A recorded wait on an RWMutex names, beside the op that held it, the kind
// of work of the writer that let it in; the dump writes it as the wait's
// class.
func TestRecordedWaitNamesTheWriterKind(t *testing.T) {
	r := withTestRecorder(t, 0)
	holderCtx, _ := BeginOp(context.Background(), OpKindRemoteCache, "remotecache.merge.commit", OpOpts{})
	waiterCtx, _ := BeginOp(context.Background(), OpKindLazy, "Container.withExec", OpOpts{})
	var mu RWMutex
	WriteLockAs(holderCtx, &mu, "test.mu", testHolder)
	w := lockWait(t, r, waiterCtx, &mu, mu.Unlock)
	if got := r.strings.snapshot()[w.ClassID]; got != "test.commit" {
		t.Fatalf("wait %+v: its class names %q, want test.commit", w, got)
	}
	if d := toDumpEvent(w); d.ClassID != w.ClassID {
		t.Fatalf("dump %+v: want the kind as its class", d)
	}
}

// An acquisition that doesn't wait allocates nothing and records nothing,
// with no recorder: what every engine that doesn't record pays.
func TestUncontendedAcquisitionsCostNoAllocation(t *testing.T) {
	withoutRecorder(t)
	ctx := context.Background()
	var mu RWMutex
	allocs := testing.AllocsPerRun(1000, func() {
		if lw := ReadLockWaited(ctx, &mu, "test.mu"); lw != (LockWait{}) {
			t.Fatalf("an uncontended read waited: %+v", lw)
		}
		mu.RUnlock()
		if lw := WriteLockAs(ctx, &mu, "test.mu", testHolder); lw != (LockWait{}) {
			t.Fatalf("an uncontended write waited: %+v", lw)
		}
		mu.Unlock()
	})
	if allocs != 0 {
		t.Fatalf("%v allocations per uncontended read and write", allocs)
	}
}

// BenchmarkRWMutex measures a write and a read of the lock that don't wait,
// and writers and readers contending on every core, for a sync.RWMutex and
// for an RWMutex taken as an engine with no recorder takes the graph lock.
func BenchmarkRWMutex(b *testing.B) {
	withoutRecorderB(b)
	ctx := context.Background()
	b.Run("sync/uncontended", func(b *testing.B) {
		var mu sync.RWMutex
		for b.Loop() {
			mu.Lock()
			mu.Unlock() //nolint:staticcheck // SA2001: the lock's own cost.
			mu.RLock()
			mu.RUnlock() //nolint:staticcheck // SA2001: the lock's own cost.
		}
	})
	b.Run("wcprof/uncontended", func(b *testing.B) {
		var mu RWMutex
		for b.Loop() {
			WriteLockAs(ctx, &mu, "bench.mu", testHolder)
			mu.Unlock()
			ReadLockWaited(ctx, &mu, "bench.mu")
			mu.RUnlock()
		}
	})
	b.Run("sync/contended", func(b *testing.B) {
		var mu sync.RWMutex
		var n atomic.Int64
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if n.Add(1)%8 == 0 {
					mu.Lock()
					mu.Unlock() //nolint:staticcheck // SA2001: the lock's own cost.
					continue
				}
				mu.RLock()
				mu.RUnlock() //nolint:staticcheck // SA2001: the lock's own cost.
			}
		})
	})
	b.Run("wcprof/contended", func(b *testing.B) {
		var mu RWMutex
		var n atomic.Int64
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if n.Add(1)%8 == 0 {
					WriteLockAs(ctx, &mu, "bench.mu", testHolder)
					mu.Unlock()
					continue
				}
				ReadLockWaited(ctx, &mu, "bench.mu")
				mu.RUnlock()
			}
		})
	})
}

func withoutRecorderB(b *testing.B) {
	prev, prevOn := global.Load(), globalOn.Load()
	global.Store(nil)
	globalOn.Store(false)
	b.Cleanup(func() {
		global.Store(prev)
		globalOn.Store(prevOn)
	})
}
