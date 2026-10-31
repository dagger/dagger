package server

import (
	"cmp"
	"fmt"
	"slices"
	"sync"
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
// first; and the rows its bundle carries that exports had found busy, each
// with what last kept it busy and for how long, longest first.
type busyReport struct {
	rows    int
	named   []string
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
	}
	byRow := map[uint64]*held{}
	var rows []*held
	for _, root := range left {
		busy := root.Busy
		if len(busy) == 0 && root.Kind != "" {
			busy = []dagql.BusyRow{{Row: root.Row, Kind: root.Kind}}
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
				h = &held{row: row.Row, kind: row.Kind, age: now.Sub(seen.since)}
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
// busy, counted, and named while there are any.
func (r busyReport) attrs() []any {
	attrs := []any{"busy_rows", r.rows}
	if len(r.named) > 0 {
		attrs = append(attrs, "busy_named", r.named)
	}
	attrs = append(attrs, "busy_cleared", r.cleared)
	if len(r.freed) > 0 {
		attrs = append(attrs, "busy_cleared_named", r.freed)
	}
	return attrs
}
