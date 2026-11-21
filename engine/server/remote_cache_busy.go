package server

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql"
)

// maxBusyRows bounds the rows an adapter remembers as busy: past it, a row
// newly found busy isn't remembered, and its age reads 0.
const maxBusyRows = 4096

// maxLoggedBusyRows is how many busy rows, and how many rows no longer busy,
// an export's log line names: past it, they are only counted.
const maxLoggedBusyRows = 8

// busyRows remembers, for one adapter, when an export first found each row
// busy, and what kept it busy when last found, until a bundle carries the
// row: so each export's log line says how long its busy rows have been busy,
// and how long the rows it now carries were.
type busyRows struct {
	mu   sync.Mutex
	rows map[uint64]busyRow
}

type busyRow struct {
	kind  dagql.BusyKind
	since time.Time
}

// busyReport is what an export's log line says of busy rows: the rows that
// kept its roots out, each with what kept it busy, how long exports have
// found it busy, and how many of the export's roots it kept out, most roots
// first, and, for each of those it names, the capture's own error for it:
// which check found it busy. Then the rows its bundle carries that exports
// had found busy, each with what last kept it busy and for how long, longest
// first.
type busyReport struct {
	rows    int
	named   []string
	reasons []string
	cleared int
	freed   []string
}

// observe records an export's left-out roots and the rows its bundle
// carries, at now, and reports them. Each busy row a left-out root reaches
// counts that root; a refused root's row isn't busy.
func (b *busyRows) observe(now time.Time, left []dagql.LeftOutRoot, carried []uint64) busyReport {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rows == nil {
		b.rows = map[uint64]busyRow{}
	}
	type held struct {
		row   uint64
		kind  dagql.BusyKind
		age   time.Duration
		roots int
		err   error
	}
	byRow := map[uint64]*held{}
	var rows []*held
	for _, root := range left {
		busy := root.Busy
		if len(busy) == 0 && root.Kind != "" {
			busy = []dagql.BusyRow{{Row: root.Row, Kind: root.Kind, Err: root.Err}}
		}
		for _, row := range busy {
			h := byRow[row.Row]
			if h == nil {
				seen, ok := b.rows[row.Row]
				switch {
				case ok:
					seen.kind = row.Kind
					b.rows[row.Row] = seen
				case len(b.rows) < maxBusyRows:
					seen = busyRow{kind: row.Kind, since: now}
					b.rows[row.Row] = seen
				default:
					seen = busyRow{kind: row.Kind, since: now}
				}
				h = &held{row: row.Row, kind: row.Kind, age: now.Sub(seen.since), err: row.Err}
				byRow[row.Row] = h
				rows = append(rows, h)
			}
			h.roots++
		}
	}
	slices.SortFunc(rows, func(x, y *held) int {
		return cmp.Or(cmp.Compare(y.roots, x.roots), cmp.Compare(x.row, y.row))
	})
	report := busyReport{rows: len(rows)}
	for _, h := range rows[:min(len(rows), maxLoggedBusyRows)] {
		report.named = append(report.named, fmt.Sprintf("%d:%s:%s:%d", h.row, h.kind, h.age.Round(time.Millisecond), h.roots))
		if h.err != nil {
			report.reasons = append(report.reasons, fmt.Sprintf("%d:%s", h.row, h.err))
		}
	}
	type freed struct {
		row     uint64
		kind    dagql.BusyKind
		busyFor time.Duration
	}
	var cleared []freed
	for _, row := range carried {
		seen, ok := b.rows[row]
		if !ok {
			continue
		}
		delete(b.rows, row)
		cleared = append(cleared, freed{row: row, kind: seen.kind, busyFor: now.Sub(seen.since)})
	}
	slices.SortFunc(cleared, func(x, y freed) int {
		return cmp.Or(cmp.Compare(y.busyFor, x.busyFor), cmp.Compare(x.row, y.row))
	})
	report.cleared = len(cleared)
	for _, f := range cleared[:min(len(cleared), maxLoggedBusyRows)] {
		report.freed = append(report.freed, fmt.Sprintf("%d:%s:%s", f.row, f.kind, f.busyFor.Round(time.Millisecond)))
	}
	return report
}

// attrs are the report's log attributes: the busy rows and those no longer
// busy, counted, and named while there are any, with the named busy rows'
// reasons.
func (r busyReport) attrs() []any {
	attrs := []any{"busy_rows", r.rows}
	if len(r.named) > 0 {
		attrs = append(attrs, "busy_named", r.named)
	}
	if len(r.reasons) > 0 {
		attrs = append(attrs, "busy_reasons", r.reasons)
	}
	attrs = append(attrs, "busy_cleared", r.cleared)
	if len(r.freed) > 0 {
		attrs = append(attrs, "busy_cleared_named", r.freed)
	}
	return attrs
}

// busyWaits are the roots exports left out busy, by number, each with the
// rows that kept it out, until none of those rows is busy any more: the
// engine then tells the service the root is ready (protocol.Ready), and the
// service asks for it again.
type busyWaits struct {
	mu    sync.Mutex
	roots map[uint64][]uint64
	// added receives as a root begins to wait, once for roots added before
	// it is taken: one a capture race kept out, with nothing in flight, is
	// ready at once.
	added chan struct{}
}

// wait makes root, left out busy by an export that asked for it as number,
// wait for the rows that kept it out: those its capture names, or its own.
func (w *busyWaits) wait(number uint64, root dagql.LeftOutRoot) {
	rows := make([]uint64, 0, max(len(root.Busy), 1))
	for _, row := range root.Busy {
		rows = append(rows, row.Row)
	}
	if len(rows) == 0 {
		rows = append(rows, root.Row)
	}
	w.mu.Lock()
	if w.roots == nil {
		w.roots = map[uint64][]uint64{}
	}
	w.roots[number] = rows
	added := w.addedLocked()
	w.mu.Unlock()
	select {
	case added <- struct{}{}:
	default:
	}
}

func (w *busyWaits) addedCh() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.addedLocked()
}

func (w *busyWaits) addedLocked() chan struct{} {
	if w.added == nil {
		w.added = make(chan struct{}, 1)
	}
	return w.added
}

// take returns the waiting roots none of whose rows busy reports busy,
// sorted, and stops their waits, and reports whether any root still waits.
// busy is called without w's lock.
func (w *busyWaits) take(busy func([]uint64) []uint64) ([]uint64, bool) {
	w.mu.Lock()
	waiting := maps.Clone(w.roots)
	w.mu.Unlock()
	if len(waiting) == 0 {
		return nil, false
	}
	var rows []uint64
	for _, r := range waiting {
		rows = append(rows, r...)
	}
	still := map[uint64]bool{}
	for _, row := range busy(rows) {
		still[row] = true
	}
	var ready []uint64
	w.mu.Lock()
	defer w.mu.Unlock()
	for number, r := range waiting {
		// A root left out again meanwhile waits for its new rows.
		if !slices.Equal(w.roots[number], r) || slices.ContainsFunc(r, func(row uint64) bool { return still[row] }) {
			continue
		}
		ready = append(ready, number)
		delete(w.roots, number)
	}
	slices.Sort(ready)
	return ready, len(w.roots) > 0
}

// TakeReadyRoots waits until some of the roots exports left out busy can be
// captured, and returns their numbers: those whose rows are no longer busy
// (dagql.Cache.BusyRows). It looks as each root begins to wait, since one a
// capture race kept out is ready at once, and after each end of what kept a
// row busy, an evaluation's or an attachment's (dagql.Cache.WatchBusyEnds),
// which is what frees the others. What its looks cost is counted
// (readyStats), and logged as it ends with its connection.
func (a *RemoteCacheAdapter) TakeReadyRoots(ctx context.Context) (_ []uint64, rerr error) {
	ends, stop := a.cache.WatchBusyEnds()
	defer stop()
	defer func() {
		if rerr != nil {
			slog.Info("remote cache ready looks", a.readyStats.snapshot().attrs()...)
		}
	}()
	busy := func(rows []uint64) []uint64 {
		start := time.Now()
		still := a.cache.BusyRows(ctx, rows)
		a.readyStats.looked(len(rows), time.Since(start))
		return still
	}
	for {
		if a.stopped.Load() {
			return nil, ErrRemoteCacheAdapterClosed
		}
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		ready, waiting := a.waits.take(busy)
		if len(ready) > 0 {
			a.readyStats.roots.Add(int64(len(ready)))
			return ready, nil
		}
		var ended <-chan struct{}
		if waiting {
			ended = ends
		}
		select {
		case <-ended:
			a.readyStats.endWakes.Add(1)
		case <-a.waits.addedCh():
			a.readyStats.addWakes.Add(1)
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
}

// readyStats are what the engine's look for ready busy roots has cost since
// it started (TakeReadyRoots): its looks, which read the waiting roots'
// rows (dagql.Cache.BusyRows), how many rows they read and the time that
// took, the graph's read lock included; what woke each look after a
// connection's first, an end of what keeps rows busy or a root beginning to
// wait; and the roots it handed on.
type readyStats struct {
	looks, rows, lookTime, endWakes, addWakes, roots atomic.Int64
}

// looked counts a look that read rows in took.
func (s *readyStats) looked(rows int, took time.Duration) {
	s.looks.Add(1)
	s.rows.Add(int64(rows))
	s.lookTime.Add(int64(took))
}

// readyStatsSnapshot is readyStats read once, lookTime in nanoseconds.
type readyStatsSnapshot struct {
	looks, rows, lookTime, endWakes, addWakes, roots int64
}

func (s *readyStats) snapshot() readyStatsSnapshot {
	return readyStatsSnapshot{
		looks: s.looks.Load(), rows: s.rows.Load(), lookTime: s.lookTime.Load(),
		endWakes: s.endWakes.Load(), addWakes: s.addWakes.Load(), roots: s.roots.Load(),
	}
}

// attrs are the snapshot's log attributes.
func (s readyStatsSnapshot) attrs() []any {
	return []any{
		"looks", s.looks, "rows", s.rows, "look_time", time.Duration(s.lookTime),
		"end_wakes", s.endWakes, "add_wakes", s.addWakes, "roots", s.roots,
	}
}
