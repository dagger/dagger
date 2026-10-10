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
	return newLargeLinkedDB(tb, fanout, depth, 0)
}

// newLargeLinkedDB is newLargeDB with a cause link on every linkEvery'th
// span (none if 0), to the span created linkEvery spans before it -- usually
// in another subtree, like a resumed span linking to the call that created
// it. Every other linked span is a passthrough, like a lazy resume.
func newLargeLinkedDB(tb testing.TB, fanout, depth, linkEvery int) (*DB, SpanID) {
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
	var ids []SpanID
	var grow func(parent SpanID, level int)
	grow = func(parent SpanID, level int) {
		for range fanout {
			id := newID()
			snap := SpanSnapshot{
				ID: id, TraceID: traceID, ParentID: parent, Name: "step",
				StartTime: start, EndTime: start.Add(time.Second),
			}
			if linkEvery > 0 && len(ids)%linkEvery == linkEvery-1 {
				snap.Links = []SpanLink{causeLink(ids[len(ids)-linkEvery+1])}
				snap.Passthrough = len(ids)%(2*linkEvery) == linkEvery-1
			}
			ids = append(ids, id)
			snaps = append(snaps, snap)
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

// newAgentDB builds the shape of a long `dagger agent` session as the TUI
// sees it: a running host span with `turns` conversation turns, each a user
// prompt and an assistant reply, with a tool call parented under the reply
// that hides fanout^1 + ... + fanout^depth completed spans of work. The
// conversation is promoted onto the host, which is marked passthrough, as
// frontendPretty.promoteConversationLocked does, so a view zoomed to the host
// lists the revealed turns.
func newAgentDB(tb testing.TB, turns, fanout, depth int) (*DB, SpanID) {
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
	at := func() time.Time {
		return start.Add(time.Duration(next) * time.Microsecond)
	}
	hostID := newID()
	snaps := []SpanSnapshot{{ID: hostID, TraceID: traceID, Name: "agent", StartTime: start}}
	var grow func(parent SpanID, level int)
	grow = func(parent SpanID, level int) {
		for range fanout {
			id := newID()
			snaps = append(snaps, SpanSnapshot{
				ID: id, TraceID: traceID, ParentID: parent, Name: "step",
				StartTime: at(), EndTime: at().Add(time.Second),
			})
			if level+1 < depth {
				grow(id, level+1)
			}
		}
	}
	for range turns {
		promptID := newID()
		snaps = append(snaps, SpanSnapshot{
			ID: promptID, TraceID: traceID, ParentID: hostID, Name: "prompt",
			LLMRole: "user", Message: "do the thing",
			StartTime: at(), EndTime: at(),
		})
		replyID := newID()
		snaps = append(snaps, SpanSnapshot{
			ID: replyID, TraceID: traceID, ParentID: hostID, Name: "reply",
			LLMRole: "assistant", Message: "on it",
			StartTime: at(), EndTime: at().Add(time.Minute),
		})
		toolID := newID()
		snaps = append(snaps, SpanSnapshot{
			ID: toolID, TraceID: traceID, ParentID: replyID, Name: "tool",
			LLMRole: "assistant", LLMTool: "run",
			StartTime: at(), EndTime: at().Add(time.Minute),
		})
		grow(toolID, 0)
	}
	db.ImportSnapshots(snaps)
	db.SetPrimarySpan(hostID)
	host := db.Spans.Map[hostID]
	db.PromoteConversationTo(host)
	host.Passthrough = true
	return db, hostID
}

// BenchmarkRowsView measures a view rebuild: RowsView and its Rows. It
// reports how many trees the rebuild builds against how many rows it shows.
func BenchmarkRowsView(b *testing.B) {
	db, rootID := newLargeDB(b, 10, 5) // ~111k spans
	opts := FrontendOpts{Verbosity: ShowCompletedVerbosity, GCThreshold: time.Hour}
	run := func(b *testing.B, db *DB, opts FrontendOpts) {
		for b.Loop() {
			db.RowsView(opts).Rows(opts)
		}
		view := db.RowsView(opts)
		b.ReportMetric(float64(len(view.BySpan)), "trees/op")
		b.ReportMetric(float64(len(view.Rows(opts).Order)), "rows/op")
	}
	b.Run("all", func(b *testing.B) {
		run(b, db, opts)
	})
	b.Run("zoomed", func(b *testing.B) {
		opts := opts
		opts.ZoomedSpan = rootID
		run(b, db, opts)
	})
	b.Run("linked", func(b *testing.B) {
		db, rootID := newLargeLinkedDB(b, 10, 5, 20)
		opts := opts
		opts.ZoomedSpan = rootID
		run(b, db, opts)
	})
	b.Run("agent", func(b *testing.B) {
		db, hostID := newAgentDB(b, 100, 10, 3) // ~111k spans
		opts := opts
		opts.ZoomedSpan = hostID
		run(b, db, opts)
	})
}
