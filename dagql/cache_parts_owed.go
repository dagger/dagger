package dagql

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// owedPartReports are the entries a span reported only some of the parts
// of: its read couldn't read the entry's record then, as while another read
// held the record's object (a Container's capture guard is exclusive), and
// returned the parts the entry's gate had settled (completePartsFellBack).
// The span reports them as the entry's parts, so the remote cache believes
// the engine lacks the rest: it never asks this engine to export them, and a
// part only this engine holds is never stored. Each such entry owes a report
// of its complete parts, which the cache makes through the share report
// (WithSnapshotShareReport) once the entry's record can be read: the remote
// cache takes a later report of the same value at the same replacement count
// as more of its parts.
//
// The cache tries again, on its worker, after each lazy evaluation finishes,
// since an evaluation is what holds an entry's record and what changes it,
// and finally at the cache's close, once the engine's sessions are gone and
// before the cache stops admitting reads (closeOwedPartReports). The engine's
// events, which carry the share report, stop only after the cache closes. Nothing waits on a timer: an engine that goes idle
// with an entry still owed reports it at its next evaluation or its close.
// An engine killed before then never reports it; its cache is wiped at the
// next start, and the remote cache releases its identity with everything
// it held.
type owedPartReports struct {
	mu   sync.Mutex
	rows map[sharedResultID]*sharedResult
	// order is the owed entries in the order they first owed.
	order   []sharedResultID
	wake    chan struct{}
	started bool
	stop    context.CancelFunc
	done    chan struct{}
}

// owePartsReport notes that a span reported only the settled parts of row.
// It does nothing on a cache without the share report, whose spans no remote
// cache reads.
func (c *Cache) owePartsReport(row *sharedResult) {
	if c == nil || row == nil || c.shareReport == nil {
		return
	}
	o := &c.owedParts
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.rows == nil {
		o.rows = map[sharedResultID]*sharedResult{}
	}
	if _, ok := o.rows[row.id]; ok {
		return
	}
	o.rows[row.id] = row
	o.order = append(o.order, row.id)
	if !o.started {
		o.started = true
		o.wake = make(chan struct{}, 1)
		o.done = make(chan struct{})
		ctx, stop := context.WithCancel(ContextWithCache(context.Background(), c))
		o.stop = stop
		go c.runOwedPartReports(ctx)
	}
}

// wakeOwedPartReports asks the worker to try the owed reports again. It never
// blocks.
func (c *Cache) wakeOwedPartReports() {
	o := &c.owedParts
	o.mu.Lock()
	wake := o.wake
	o.mu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (c *Cache) runOwedPartReports(ctx context.Context) {
	defer close(c.owedParts.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.owedParts.wake:
			c.reportOwedParts(ctx)
		}
	}
}

// reportOwedParts reports the complete parts of each owed entry whose record
// it can read now, and returns how many entries still owe. An entry the
// cache no longer holds, or whose value was replaced since, owes nothing: a
// replaced value's own spans report it. One whose record can't be read for
// another reason than its state not being ready owes nothing either: no read
// can describe that record, so the settled parts are all any report can
// name. That is the one case in which a span's subset stands, and it is
// logged.
func (c *Cache) reportOwedParts(ctx context.Context) int {
	o := &c.owedParts
	o.mu.Lock()
	order := append([]sharedResultID(nil), o.order...)
	rows := make([]*sharedResult, 0, len(order))
	for _, id := range order {
		rows = append(rows, o.rows[id])
	}
	o.mu.Unlock()

	var parts []SnapshotSharedPart
	var settled []sharedResultID
	for _, row := range rows {
		c.egraphMu.RLock()
		held, replacements := c.resultsByID[row.id] == row, row.replacements
		c.egraphMu.RUnlock()
		if !held {
			settled = append(settled, row.id)
			continue
		}
		keys, _, err := c.completeParts(ctx, row, c.completePartSizeReader())
		switch {
		case errors.Is(err, ErrPersistStateNotReady):
			continue
		case err != nil:
			slog.WarnContext(ctx, "owed parts report dropped: the entry's record can't be described", "row", uint64(row.id), "cause", err)
			settled = append(settled, row.id)
			continue
		}
		c.egraphMu.RLock()
		if c.resultsByID[row.id] == row && row.replacements == replacements {
			deps := sortedResultIDs(row.deps)
			for _, key := range keys {
				parts = append(parts, SnapshotSharedPart{ResultID: uint64(row.id), Part: key, Deps: deps, Replacements: replacements})
			}
		}
		c.egraphMu.RUnlock()
		settled = append(settled, row.id)
	}

	o.mu.Lock()
	for _, id := range settled {
		delete(o.rows, id)
	}
	// The order kept, of the entries still owed, those owed during this
	// pass among them.
	kept := o.order[:0]
	for _, id := range o.order {
		if _, ok := o.rows[id]; ok {
			kept = append(kept, id)
		}
	}
	o.order = kept
	left := len(o.rows)
	o.mu.Unlock()
	if len(parts) > 0 {
		c.shareReport(parts)
	}
	return left
}

// closeOwedPartReports stops the worker and makes the last owed reports. It
// runs at the cache's close, after the engine's sessions are gone, so no
// evaluation of theirs holds a record; an entry whose record still can't be
// read is logged.
func (c *Cache) closeOwedPartReports(ctx context.Context) {
	if !c.stopOwedPartReports() {
		return
	}
	if left := c.reportOwedParts(ctx); left > 0 {
		slog.WarnContext(ctx, "owed parts reports left at close", "entries", left)
	}
}

// stopOwedPartReports stops the worker, if it ran, and waits for it to end.
// It reports whether it ran.
func (c *Cache) stopOwedPartReports() bool {
	o := &c.owedParts
	o.mu.Lock()
	stop, done := o.stop, o.done
	o.mu.Unlock()
	if stop == nil {
		return false
	}
	stop()
	<-done
	return true
}
