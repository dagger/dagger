package server

import (
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
// carries, at now, and reports them. A refused root's row isn't busy.
func (b *busyRows) observe(now time.Time, left []dagql.LeftOutRoot, carried []uint64) busyReport {
	return busyReport{}
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
