package dagql

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
)

// owedPartReports are the entries a span reported only some of the parts
// of: its read couldn't read the entry's record then, as while another read
// held the record's object (a Container's capture guard is exclusive), and
// returned the parts the entry's gate had settled (completePartsFellBack).
// The span reports them as the entry's parts, so the remote cache believes
// the engine lacks the rest: it never asks this engine to export them, and a
// part only this engine holds is never stored. Each such entry owes a report
// of the complete parts its spans left out, with their raw sizes as a span
// reports them, which the cache makes through the share report
// (WithSnapshotShareReport) once the entry's record can be read: the remote
// cache takes a later report of the same value at the same replacement count
// as more of its parts. A later span that reads the record whole reports
// every part itself, and settles what the entry owed.
//
// The cache tries again, on its worker, after each lazy evaluation finishes,
// since an evaluation is what holds an entry's record and what changes it,
// and finally at the cache's close, once the engine's sessions are gone and
// before the cache stops admitting reads (closeOwedPartReports). The engine's
// events, which carry the share report, stop only after the cache closes.
// Nothing waits on a timer: an engine that goes idle with an entry still owed
// reports it at its next evaluation or its close.
// An engine killed before then never reports it; its cache is wiped at the
// next start, and the remote cache releases its identity with everything
// it held.
type owedPartReports struct {
	mu   sync.Mutex
	rows map[sharedResultID]owedPartReport
	// order is the owed entries in the order they first owed.
	order []sharedResultID
	// seq numbers each time an entry owes, so a pass, or a span's read,
	// clears only what it served: an entry that owes again after a pass or
	// a span read it, its value replaced or not, still owes. Written under
	// mu; a span's read loads it before it reads.
	seq atomic.Uint64
	// owing is len(rows), kept for a span's read to see without the lock
	// that nothing is owed, as is nearly always so.
	owing   atomic.Int64
	wake    chan struct{}
	started bool
	stop    context.CancelFunc
	done    chan struct{}
}

// owedPartReport is one entry's owed report, as of its latest owing: the
// value it is owed for, by replacement count, and the parts the entry's
// spans of that value reported meanwhile, which it needn't name again.
type owedPartReport struct {
	row          *sharedResult
	seq          uint64
	replacements uint64
	reported     map[string]struct{}
}

// owePartsReport notes that a span reported only reported, the settled
// parts of row. It does nothing on a cache without the share report, whose
// spans no remote cache reads.
func (c *Cache) owePartsReport(row *sharedResult, reported []string) {
	if c == nil || row == nil || c.shareReport == nil {
		return
	}
	c.egraphMu.RLock()
	replacements := row.replacements
	c.egraphMu.RUnlock()
	o := &c.owedParts
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.rows == nil {
		o.rows = map[sharedResultID]owedPartReport{}
	}
	prev, ok := o.rows[row.id]
	if !ok {
		o.order = append(o.order, row.id)
	}
	// A new map, never the one an earlier owing kept: a pass reads that
	// one outside the lock.
	report := owedPartReport{row: row, replacements: replacements, reported: map[string]struct{}{}}
	if ok && prev.row == row && prev.replacements == replacements {
		for key := range prev.reported {
			report.reported[key] = struct{}{}
		}
	}
	for _, key := range reported {
		report.reported[key] = struct{}{}
	}
	report.seq = o.seq.Add(1)
	o.rows[row.id] = report
	o.owing.Store(int64(len(o.rows)))
	if !o.started {
		o.started = true
		o.wake = make(chan struct{}, 1)
		o.done = make(chan struct{})
		ctx, stop := context.WithCancel(ContextWithCache(context.Background(), c))
		o.stop = stop
		go c.runOwedPartReports(ctx)
	}
}

// owedPartsSeq is the owings so far, which a span's read loads before it
// reads, for settleOwedPartsReport.
func (c *Cache) owedPartsSeq() uint64 {
	return c.owedParts.seq.Load()
}

// settleOwedPartsReport notes that a span reported every complete part of
// row, read from its record by a read that began once seq owings were made:
// what row owed by then, that span has reported. An owing made since, of
// parts the read may not have seen, still stands.
func (c *Cache) settleOwedPartsReport(row *sharedResult, seq uint64) {
	o := &c.owedParts
	if o.owing.Load() == 0 {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if report, ok := o.rows[row.id]; ok && report.row == row && report.seq <= seq {
		delete(o.rows, row.id)
		o.owing.Store(int64(len(o.rows)))
		// Out of the order too, so an owing made again joins it once.
		o.order = slices.DeleteFunc(o.order, func(id sharedResultID) bool { return id == row.id })
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

// reportOwedParts reports, for each owed entry whose record it can read now,
// the complete parts its spans left out, with their raw sizes when it reads
// them, and returns how many entries still owe. An entry the cache no longer
// holds, or whose value was replaced since it owed, owes nothing: a replaced
// value's own spans report it. One whose record can't be read for
// another reason than its state not being ready owes nothing either: no read
// can describe that record, so the settled parts are all any report can
// name. That is the one case in which a span's subset stands, and it is
// logged.
func (c *Cache) reportOwedParts(ctx context.Context) int {
	o := &c.owedParts
	o.mu.Lock()
	owed := make([]owedPartReport, 0, len(o.order))
	for _, id := range o.order {
		// An entry a span's read settled is in the order until a pass
		// tidies it.
		if report, ok := o.rows[id]; ok {
			owed = append(owed, report)
		}
	}
	o.mu.Unlock()

	var parts []SnapshotSharedPart
	var settled []owedPartReport
	for _, report := range owed {
		row := report.row
		c.egraphMu.RLock()
		held := c.resultsByID[row.id] == row && row.replacements == report.replacements
		c.egraphMu.RUnlock()
		if !held {
			settled = append(settled, report)
			continue
		}
		keys, sizes, err := c.completeParts(ctx, row, c.completePartSizeReader())
		if c.testOwedPartsRead != nil {
			c.testOwedPartsRead(row)
		}
		switch {
		case errors.Is(err, ErrPersistStateNotReady), err != nil && ctx.Err() != nil:
			// Not readable yet, or this pass was stopped, as the worker is at
			// close: the entry still owes, for a later pass or the final one.
			continue
		case err != nil:
			slog.WarnContext(ctx, "owed parts report dropped: the entry's record can't be described", "row", uint64(row.id), "cause", err)
			settled = append(settled, report)
			continue
		}
		c.egraphMu.RLock()
		if c.resultsByID[row.id] == row && row.replacements == report.replacements {
			deps := sortedResultIDs(row.deps)
			for i, key := range keys {
				if _, ok := report.reported[key]; ok {
					continue
				}
				part := SnapshotSharedPart{ResultID: uint64(row.id), Part: key, Deps: deps, Replacements: report.replacements}
				if sizes != nil {
					part.RawSize = &sizes[i]
				}
				parts = append(parts, part)
			}
		}
		c.egraphMu.RUnlock()
		settled = append(settled, report)
	}

	o.mu.Lock()
	for _, report := range settled {
		// An entry that owed again since this pass read it still owes.
		if o.rows[report.row.id].seq == report.seq {
			delete(o.rows, report.row.id)
		}
	}
	o.owing.Store(int64(len(o.rows)))
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
