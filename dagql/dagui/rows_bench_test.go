package dagui

import (
	"encoding/binary"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// newLargeDB builds a DB of fanout^1 + ... + fanout^depth completed spans
// under a running root, returning the DB and the root's ID.
func newLargeDB(tb testing.TB, fanout, depth int) (*DB, SpanID) {
	tb.Helper()
	db := NewDB()
	traceID := TraceID{TraceID: trace.TraceID{1}}
	start := time.Unix(100, 0)
	var next uint64
	newID := func() SpanID {
		next++
		var id trace.SpanID
		binary.BigEndian.PutUint64(id[:], next)
		return SpanID{SpanID: id}
	}
	rootID := newID()
	snaps := []SpanSnapshot{{ID: rootID, TraceID: traceID, Name: "root", StartTime: start}}
	var grow func(parent SpanID, level int)
	grow = func(parent SpanID, level int) {
		for range fanout {
			id := newID()
			snaps = append(snaps, SpanSnapshot{
				ID: id, TraceID: traceID, ParentID: parent, Name: "step",
				StartTime: start, EndTime: start.Add(time.Second),
			})
			if level+1 < depth {
				grow(id, level+1)
			}
		}
	}
	grow(rootID, 0)
	db.ImportSnapshots(snaps)
	db.SetPrimarySpan(rootID)
	return db, rootID
}

func BenchmarkRowsView(b *testing.B) {
	db, rootID := newLargeDB(b, 10, 5) // ~111k spans
	opts := FrontendOpts{Verbosity: ShowCompletedVerbosity, GCThreshold: time.Hour}
	b.Run("all", func(b *testing.B) {
		for b.Loop() {
			db.RowsView(opts).Rows(opts)
		}
	})
	b.Run("zoomed", func(b *testing.B) {
		opts := opts
		opts.ZoomedSpan = rootID
		for b.Loop() {
			db.RowsView(opts).Rows(opts)
		}
	})
}
