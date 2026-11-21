package idtui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

// newWaitChainDB builds groups copies of the lazy-evaluation shape the engine
// emits (see buildLazyChainDB in dagui): a Container.stdout whose call_exec
// twin waits on a resume of one withExec, which in turn waits on a resume of
// another withExec running a real exec. Every row's duration is then a
// non-trivial TimeBreakdown. With running, the root and each group's stdout
// row are still running.
func newWaitChainDB(groups int, running bool) (*dagui.DB, []dagui.SpanSnapshot, []*dagui.Span) {
	db := dagui.NewDB()
	traceID := dagui.TraceID{TraceID: trace.TraceID{1}}
	epoch := time.Unix(100, 0)
	at := func(sec float64) time.Time { return epoch.Add(time.Duration(sec * float64(time.Second))) }
	var next uint64
	newID := func() dagui.SpanID {
		id := perfSpanID(next)
		next++
		return id
	}
	waitLink := func(target dagui.SpanID, from, to time.Time) dagui.SpanLink {
		return dagui.SpanLink{
			SpanContext: dagui.SpanContext{TraceID: traceID, SpanID: target},
			Purpose:     "wait", WaitReason: "lazy", WaitStart: from, WaitEnd: to,
		}
	}
	causeLink := func(target dagui.SpanID) dagui.SpanLink {
		return dagui.SpanLink{SpanContext: dagui.SpanContext{TraceID: traceID, SpanID: target}, Purpose: "cause"}
	}
	end := func(t time.Time) time.Time {
		if running {
			return time.Time{}
		}
		return t
	}
	root := newID()
	snaps := []dagui.SpanSnapshot{{ID: root, TraceID: traceID, Name: "query", StartTime: at(0), EndTime: end(at(16))}}
	for range groups {
		w2, r2, e2, w1, s, x, r1 := newID(), newID(), newID(), newID(), newID(), newID(), newID()
		exec := dagui.SpanSnapshot{ID: e2, TraceID: traceID, ParentID: w2, Name: "exec go build", StartTime: at(3), EndTime: at(13)}
		exec.ProcessAttribute("wcprof.exec.argv", `["go","build","-o","/tmp/waitdemo","."]`)
		snaps = append(snaps,
			dagui.SpanSnapshot{ID: w2, TraceID: traceID, ParentID: root, Name: "Container.withExec", StartTime: at(1), EndTime: at(1.5)},
			dagui.SpanSnapshot{ID: r2, TraceID: traceID, ParentID: w2, Name: "resume withExec", StartTime: at(3), EndTime: at(13),
				Links: []dagui.SpanLink{causeLink(w2)}},
			exec,
			dagui.SpanSnapshot{ID: w1, TraceID: traceID, ParentID: root, Name: "Container.withExec", StartTime: at(2), EndTime: at(2.5)},
			dagui.SpanSnapshot{ID: s, TraceID: traceID, ParentID: root, Name: "Container.stdout", StartTime: at(3), EndTime: end(at(16))},
			dagui.SpanSnapshot{ID: x, TraceID: traceID, ParentID: s, Name: "Container.stdout", StartTime: at(3), EndTime: end(at(16)),
				Links: []dagui.SpanLink{waitLink(r1, at(3), at(16))}},
			dagui.SpanSnapshot{ID: r1, TraceID: traceID, ParentID: x, Name: "resume withExec", StartTime: at(3), EndTime: end(at(16)),
				Links: []dagui.SpanLink{causeLink(w1), waitLink(r2, at(3), at(13))}},
		)
	}
	db.ImportSnapshots(snaps)
	var rows []*dagui.Span
	for _, snap := range snaps {
		rows = append(rows, db.Spans.Map[snap.ID])
	}
	return db, snaps, rows
}

// BenchmarkRenderSpanDuration measures one frame's worth of row durations
// (renderSpanDuration, which every frame runs for every visible row) over a
// trace of 50 wait-chain groups. idle frames see no span updates between
// them, the common case for a TUI left open; streaming frames each follow a
// span update.
func BenchmarkRenderSpanDuration(b *testing.B) {
	for _, running := range []bool{false, true} {
		for _, streaming := range []bool{false, true} {
			name := fmt.Sprintf("running=%t/streaming=%t", running, streaming)
			b.Run(name, func(b *testing.B) {
				db, snaps, rows := newWaitChainDB(50, running)
				update := snaps[len(snaps)-1:]
				buf := new(strings.Builder)
				out := NewOutput(buf, termenv.WithProfile(termenv.Ascii))
				now := time.Unix(200, 0)
				for b.Loop() {
					if streaming {
						db.ImportSnapshots(update)
					}
					now = now.Add(16 * time.Millisecond)
					buf.Reset()
					for _, row := range rows {
						renderSpanDuration(out, row, now, false)
					}
				}
			})
		}
	}
}
