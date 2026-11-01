package wcprof

import (
	"context"
	"runtime"
	"testing"
)

// lockWait has one goroutine take mu with WriteLock under ctx while the
// test holds it, and returns the wait it recorded. The test holds mu on
// entry; lockWait releases it.
func lockWait(t *testing.T, r *Recorder, ctx context.Context, mu *RWMutex, unlock func()) Event {
	t.Helper()
	locked := make(chan struct{})
	go func() {
		WriteLock(ctx, mu, "test.mu")
		close(locked)
	}()
	for !waitingOn(r) {
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
