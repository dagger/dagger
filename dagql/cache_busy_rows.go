package dagql

import (
	"context"
	"sync"

	"github.com/dagger/dagger/engine/wcprof"
)

// evaluationEnds are the watches of the cache's lazy evaluations' ends
// (WatchEvaluationEnds). Each watch has a channel of one slot, so ends that
// come before the watcher takes one wake it once.
type evaluationEnds struct {
	mu      sync.Mutex
	watches map[chan struct{}]struct{}
}

// WatchEvaluationEnds returns a channel that receives after each of the
// cache's lazy evaluation attempts ends, once for ends that come before it
// is taken, and a func that ends the watch. An attempt has ended when it no
// longer keeps its row busy (BusyRows), as a waiter on it is woken: a capture
// the attempt kept a row out of may take the row when the channel receives.
func (c *Cache) WatchEvaluationEnds() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	e := &c.evaluationEnds
	e.mu.Lock()
	if e.watches == nil {
		e.watches = map[chan struct{}]struct{}{}
	}
	e.watches[ch] = struct{}{}
	e.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			e.mu.Lock()
			delete(e.watches, ch)
			e.mu.Unlock()
		})
	}
}

// evaluationEnded wakes the watches of evaluations' ends. It never blocks.
func (c *Cache) evaluationEnded() {
	e := &c.evaluationEnds
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.watches {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// BusyRows returns those of rows, entries by number, that a capture would
// leave out as busy now (ErrPersistStateNotReady) for what is in flight on
// them: their dependencies' attachment is open, or an evaluation of them
// runs or its bookkeeping is pending (lazyEvaluationInFlightLocked). A row
// the cache no longer has isn't busy, nor is one a capture found busy only
// in a race with a change that has since ended, such as a row that changed
// during the capture.
func (c *Cache) BusyRows(ctx context.Context, rows []uint64) []uint64 {
	held := make([]*sharedResult, 0, len(rows))
	wcprof.ReadLock(ctx, &c.egraphMu, egraphLockIdent)
	for _, number := range rows {
		if res := c.resultsByID[sharedResultID(number)]; res != nil {
			held = append(held, res)
		}
	}
	c.egraphMu.RUnlock()
	var busy []uint64
	for _, res := range held {
		if res.attachmentState() == resultAttachmentOpen {
			busy = append(busy, uint64(res.id))
			continue
		}
		res.lazyMu.Lock()
		err := lazyEvaluationInFlightLocked(res)
		res.lazyMu.Unlock()
		if err != nil {
			busy = append(busy, uint64(res.id))
		}
	}
	return busy
}
