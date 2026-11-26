package wcprof

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testReader = NewLockHolder("test.read")

// readHoldSeen is a hold for reading as an RWMutex's read hold observer is
// told of it.
type readHoldSeen struct {
	kind         LockHolder
	waited, held time.Duration
}

// observeReadHolds sets mu's read hold observer for the test, and returns
// what it has been told so far.
func observeReadHolds(t *testing.T, mu *RWMutex) func() []readHoldSeen {
	var (
		seenMu sync.Mutex
		seen   []readHoldSeen
	)
	stop := mu.ObserveReadHolds(func(kind LockHolder, waited, held time.Duration) {
		seenMu.Lock()
		defer seenMu.Unlock()
		seen = append(seen, readHoldSeen{kind, waited, held})
	})
	t.Cleanup(stop)
	return func() []readHoldSeen {
		seenMu.Lock()
		defer seenMu.Unlock()
		return append([]readHoldSeen(nil), seen...)
	}
}

// A hold for reading that names its kind of work is seen as it ends, with
// its kind and how long it held the lock; a reader that names none isn't.
func TestReadHoldIsTimedByKind(t *testing.T) {
	withoutRecorder(t)
	var mu RWMutex
	seen := observeReadHolds(t, &mu)
	h := mu.RLockAs(testReader)
	time.Sleep(2 * time.Millisecond)
	h.Unlock()
	mu.RLock()
	mu.RUnlock() //nolint:staticcheck // SA2001: a reader that names no kind, which the observer must not see.
	got := seen()
	if len(got) != 1 {
		t.Fatalf("seen %+v: want one hold", got)
	}
	if got[0].kind != testReader || got[0].held < 2*time.Millisecond || got[0].waited != 0 {
		t.Fatalf("seen %+v: want a hold of at least 2ms as %s that didn't wait", got[0], testReader)
	}
}

// A reader that waited for a writer is seen with that wait, which its take
// returns too, naming the writer that let it in.
func TestReadHoldCarriesItsWait(t *testing.T) {
	withoutRecorder(t)
	var mu RWMutex
	seen := observeReadHolds(t, &mu)
	WriteLockAs(context.Background(), &mu, "test.mu", testHolder)
	got := make(chan LockWait)
	go func() {
		h, lw := ReadLockAs(context.Background(), &mu, "test.mu", testReader)
		h.Unlock()
		got <- lw
	}()
	blockedIn(t, "wcprof.ReadLockAs(")
	time.Sleep(time.Millisecond)
	mu.Unlock()
	lw := <-got
	if lw.Holder != testHolder || lw.Waited < time.Millisecond {
		t.Fatalf("wait %+v: want at least 1ms behind %s", lw, testHolder)
	}
	if s := seen(); len(s) != 1 || s[0].kind != testReader || s[0].waited != lw.Waited {
		t.Fatalf("seen %+v: want one hold as %s that waited %s", s, testReader, lw.Waited)
	}
}

// A hold given up and taken again (Again) is two holds, each seen.
func TestReadHoldTakenAgainIsTwoHolds(t *testing.T) {
	withoutRecorder(t)
	var mu RWMutex
	seen := observeReadHolds(t, &mu)
	h := mu.RLockAs(testReader)
	free := false
	h.Again(func() {
		if mu.RWMutex.TryLock() {
			free = true
			mu.RWMutex.Unlock()
		}
	})
	h.Unlock()
	if !free {
		t.Fatal("the lock was held while Again let it go")
	}
	if got := seen(); len(got) != 2 || got[0].kind != testReader || got[1].kind != testReader {
		t.Fatalf("seen %+v: want two holds as %s", got, testReader)
	}
}

// Only a hold begun while the observer was set, and ended before it was
// stopped, is seen.
func TestReadHoldOutsideTheObserverIsNotSeen(t *testing.T) {
	withoutRecorder(t)
	var mu RWMutex
	before := mu.RLockAs(testReader)
	var n atomic.Int32
	stop := mu.ObserveReadHolds(func(LockHolder, time.Duration, time.Duration) { n.Add(1) })
	before.Unlock()
	during := mu.RLockAs(testReader)
	stop()
	during.Unlock()
	if n.Load() != 0 {
		t.Fatalf("seen %d holds: want none", n.Load())
	}
}

// BenchmarkReadHold measures a hold for reading, uncontended: a plain one, a
// timed one with no observer, as every engine that doesn't observe its
// holds takes it, and a timed one observed.
func BenchmarkReadHold(b *testing.B) {
	withoutRecorderB(b)
	b.Run("plain", func(b *testing.B) {
		var mu RWMutex
		for b.Loop() {
			mu.RLock()
			mu.RUnlock() //nolint:staticcheck // SA2001: the plain hold a timed one is measured against.
		}
	})
	b.Run("timed", func(b *testing.B) {
		var mu RWMutex
		for b.Loop() {
			h := mu.RLockAs(testReader)
			h.Unlock()
		}
	})
	b.Run("observed", func(b *testing.B) {
		var mu RWMutex
		var n atomic.Int64
		defer mu.ObserveReadHolds(func(_ LockHolder, _, held time.Duration) { n.Add(int64(held)) })()
		for b.Loop() {
			h := mu.RLockAs(testReader)
			h.Unlock()
		}
	})
}
