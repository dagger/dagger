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
	watches map[*busyWatch]struct{}
}

// busyWatch is one watch of busy rows' ends: its channel, and which rows'
// ends it wants.
type busyWatch struct {
	ch    chan struct{}
	wants func(row uint64) bool
}

// WatchBusyEnds returns a channel that receives after each end of what
// keeps a row busy (BusyRows), for the rows wants says the watcher waits
// on, once for ends that come before it is taken, and a func that ends the
// watch: each of the cache's lazy evaluation attempts, as a waiter on it is
// woken, and each row's attachment of its dependencies. A capture such a
// thing kept a row out of may take the row when the channel receives. wants
// is called at every end, of any row, and must be cheap and never call into
// the cache.
func (c *Cache) WatchBusyEnds(wants func(row uint64) bool) (<-chan struct{}, func()) {
	w := &busyWatch{ch: make(chan struct{}, 1), wants: wants}
	e := &c.busyEnds
	e.mu.Lock()
	if e.watches == nil {
		e.watches = map[*busyWatch]struct{}{}
	}
	e.watches[w] = struct{}{}
	e.mu.Unlock()
	var once sync.Once
	return w.ch, func() {
		once.Do(func() {
			e.mu.Lock()
			delete(e.watches, w)
			e.mu.Unlock()
		})
	}
}

// busyEnded wakes the watches that want row's end of what kept it busy. It
// never blocks.
func (c *Cache) busyEnded(row sharedResultID) {
	e := &c.busyEnds
	e.mu.Lock()
	defer e.mu.Unlock()
	for w := range e.watches {
		if !w.wants(uint64(row)) {
			continue
		}
		select {
		case w.ch <- struct{}{}:
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
