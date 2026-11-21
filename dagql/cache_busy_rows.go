package dagql

import (
	"context"
	"sync"

	"github.com/dagger/dagger/engine/wcprof"
)

// busyEnds are the watches of the ends of what keeps the cache's rows busy
// (WatchBusyEnds). Each watch has a channel of one slot, so ends that come
// before the watcher takes one wake it once.
type busyEnds struct {
	mu      sync.Mutex
	watches map[chan struct{}]struct{}
}

// WatchBusyEnds returns a channel that receives after each end of what
// keeps a row busy (BusyRows), once for ends that come before it is taken,
// and a func that ends the watch: each of the cache's lazy evaluation
// attempts, as a waiter on it is woken, and each row's attachment of its
// dependencies. A capture such a thing kept a row out of may take the row
// when the channel receives.
func (c *Cache) WatchBusyEnds() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	e := &c.busyEnds
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

// busyEnded wakes the watches of busy rows' ends. It never blocks.
func (c *Cache) busyEnded() {
	e := &c.busyEnds
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
