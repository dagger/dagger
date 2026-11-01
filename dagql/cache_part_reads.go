package dagql

import (
	"slices"
	"sync"
	"time"
)

// partReadFlushInterval is the longest a read waits to be reported.
const partReadFlushInterval = time.Second

// PartRead is a part of an entry that work read; Part is empty when it read
// the whole entry.
type PartRead struct {
	ResultID uint64
	Part     PartKey
}

// WithPartReadReport makes the cache report the parts of entries that work
// reads: those Evaluate and EvaluateParts evaluate for it, and those a
// delegated part is installed from. When work reads a part of an entry for
// the first time, the cache reports every part of the entry read so far, or
// the whole entry once work read it whole, so that each report about an
// entry is complete: a reader that missed earlier ones loses nothing. Reports
// go in batches, at most partReadFlushInterval after the read, and at the
// cache's close. report runs with no cache lock held and must not block.
func WithPartReadReport(report func([]PartRead)) CacheOption {
	return func(c *Cache) {
		c.partReads = &partReadLog{report: report, seen: map[sharedResultID]*partReadsSeen{}, pending: map[sharedResultID]struct{}{}}
	}
}

// partReadLog is what WithPartReadReport keeps: the parts of each live entry
// read so far, and the entries with reads not reported yet, in the order
// they were first read. A nil log records nothing. reporting is held through
// each flush, so reports go in the order they were taken, and a flush, as
// the cache's close makes, returns only once an earlier one, such as the
// timer's, has reported.
type partReadLog struct {
	report    func([]PartRead)
	reporting sync.Mutex
	mu        sync.Mutex
	seen      map[sharedResultID]*partReadsSeen
	pending   map[sharedResultID]struct{}
	order     []sharedResultID
	timer     *time.Timer
}

type partReadsSeen struct {
	whole bool
	parts []PartKey
}

// record notes that work read parts of entry id, or the whole of it when
// parts is empty, and arranges for the entry to be reported if that's new.
func (l *partReadLog) record(id sharedResultID, parts []PartKey) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	seen := l.seen[id]
	if seen == nil {
		seen = &partReadsSeen{}
		l.seen[id] = seen
	}
	if seen.whole {
		return
	}
	added := false
	if len(parts) == 0 {
		seen.whole, seen.parts, added = true, nil, true
	}
	for _, part := range parts {
		if seen.whole || slices.Contains(seen.parts, part) {
			continue
		}
		seen.parts = append(seen.parts, part)
		added = true
	}
	if !added {
		return
	}
	if _, ok := l.pending[id]; !ok {
		l.pending[id] = struct{}{}
		l.order = append(l.order, id)
	}
	if l.timer == nil {
		l.timer = time.AfterFunc(partReadFlushInterval, l.flush)
	}
}

// forget drops what was read of an entry the cache no longer holds.
// Requires nothing; safe under the cache's locks.
func (l *partReadLog) forget(id sharedResultID) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, id)
	delete(l.pending, id)
}

// flush reports, for each entry with reads not reported yet, every part of
// it read so far.
func (l *partReadLog) flush() {
	if l == nil {
		return
	}
	l.reporting.Lock()
	defer l.reporting.Unlock()
	l.mu.Lock()
	var reads []PartRead
	for _, id := range l.order {
		if _, ok := l.pending[id]; !ok {
			continue
		}
		seen := l.seen[id]
		if seen.whole {
			reads = append(reads, PartRead{ResultID: uint64(id)})
			continue
		}
		for _, part := range seen.parts {
			reads = append(reads, PartRead{ResultID: uint64(id), Part: part})
		}
	}
	clear(l.pending)
	l.order = nil
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.mu.Unlock()
	if len(reads) > 0 {
		l.report(reads)
	}
}
