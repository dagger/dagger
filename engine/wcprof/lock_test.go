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
