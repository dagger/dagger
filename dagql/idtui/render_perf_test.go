package idtui

import (
	"encoding/binary"
	"io"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

func perfSpanID(n uint64) dagui.SpanID {
	var id trace.SpanID
	binary.BigEndian.PutUint64(id[:], n+1)
	return dagui.SpanID{SpanID: id}
}

// newLargeTraceFrontend builds a live (interactive) frontend over a trace of
// roughly tops*fanout^depth completed spans hanging off a running root, the
// shape of a long `dagger agent` session: a handful of top-level rows, each
// hiding a big collapsed subtree. It returns the frontend and the IDs of the
// deepest spans, under which streamed spans can land.
func newLargeTraceFrontend(tb testing.TB, tops, fanout, depth int) (*frontendPretty, []dagui.SpanID, *uint64) {
	tb.Helper()
	db := dagui.NewDB()
	traceID := dagui.TraceID{TraceID: trace.TraceID{1}}
	start := time.Unix(100, 0)
	var next uint64
	newID := func() dagui.SpanID {
		id := perfSpanID(next)
		next++
		return id
	}
	rootID := newID()
	snaps := []dagui.SpanSnapshot{{
		ID: rootID, TraceID: traceID, Name: "root", StartTime: start,
	}}
	var leaves []dagui.SpanID
	var grow func(parent dagui.SpanID, level int)
	grow = func(parent dagui.SpanID, level int) {
		for range fanout {
			id := newID()
			snaps = append(snaps, dagui.SpanSnapshot{
				ID: id, TraceID: traceID, ParentID: parent, Name: "step",
				StartTime: start, EndTime: start.Add(time.Second),
			})
			if level+1 < depth {
				grow(id, level+1)
			} else {
				leaves = append(leaves, id)
			}
		}
	}
	for range tops {
		id := newID()
		snaps = append(snaps, dagui.SpanSnapshot{
			ID: id, TraceID: traceID, ParentID: rootID, Name: "top",
			StartTime: start, EndTime: start.Add(time.Second),
		})
		grow(id, 0)
	}
	db.ImportSnapshots(snaps)
	db.SetPrimarySpan(rootID)

	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 40))
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.GCThreshold = time.Hour
	fe.recalculateViewLocked()
	fe.tui.Frame()
	return fe, leaves, &next
}

// BenchmarkStreamingFrame measures live frames while spans stream into a
// large trace: every frameGap a batch lands (marking the view dirty, as
// ExportSpans does) and the TUI renders. ns/op is the render-loop time per
// frame (the gaps aren't timed); recalcs/frame is how many of those frames
// rebuilt the whole trace tree. The unpaced variant disables recalculation
// pacing, recalculating on every frame as the TUI used to.
func BenchmarkStreamingFrame(b *testing.B) {
	b.Run("paced", benchmarkStreamingFrame)
	b.Run("unpaced", func(b *testing.B) {
		defer func(prev int) { recalcPaceMinSpans = prev }(recalcPaceMinSpans)
		recalcPaceMinSpans = math.MaxInt
		benchmarkStreamingFrame(b)
	})
}

func benchmarkStreamingFrame(b *testing.B) {
	const frameGap = 25 * time.Millisecond
	fe, leaves, next := newLargeTraceFrontend(b, 50, 10, 4) // ~555k spans
	traceID := dagui.TraceID{TraceID: trace.TraceID{1}}
	start := time.Unix(100, 0)
	recalcs := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		time.Sleep(frameGap)
		b.StartTimer()
		id := perfSpanID(*next)
		*next++
		fe.db.ImportSnapshots([]dagui.SpanSnapshot{{
			ID: id, TraceID: traceID, ParentID: leaves[i%len(leaves)], Name: "streamed",
			StartTime: start, EndTime: start.Add(time.Second),
		}})
		fe.viewDirty = true
		fe.Update()
		before := fe.lastRecalcAt
		fe.tui.Frame()
		if fe.lastRecalcAt != before {
			recalcs++
		}
	}
	b.ReportMetric(float64(recalcs)/float64(b.N), "recalcs/frame")
}

func TestStreamingRecalcPacing(t *testing.T) {
	fe, _, next := newLargeTraceFrontend(t, 2, 2, 1)
	traceID := dagui.TraceID{TraceID: trace.TraceID{1}}
	rootID := perfSpanID(0)
	start := time.Unix(100, 0)
	stream := func() dagui.SpanID {
		id := perfSpanID(*next)
		*next++
		fe.db.ImportSnapshots([]dagui.SpanSnapshot{{
			ID: id, TraceID: traceID, ParentID: rootID, Name: "streamed",
			StartTime: start, EndTime: start.Add(time.Second),
		}})
		fe.viewDirty = true
		fe.Update()
		return id
	}
	require.NotNil(t, fe.rows.BySpan[rootID])

	t.Run("small traces recalculate every frame", func(t *testing.T) {
		fe.lastRecalcCost = time.Hour
		fe.lastRecalcAt = time.Now()
		id := stream()
		fe.tui.Frame()
		require.False(t, fe.viewDirty)
		require.NotNil(t, fe.rows.BySpan[id])
	})

	t.Run("expensive recalculation is deferred until due", func(t *testing.T) {
		defer func(spans int, cost time.Duration) {
			recalcPaceMinSpans, recalcPaceMinCost = spans, cost
		}(recalcPaceMinSpans, recalcPaceMinCost)
		recalcPaceMinSpans, recalcPaceMinCost = 0, 0

		fe.lastRecalcCost = 100 * time.Millisecond
		fe.lastRecalcAt = time.Now()
		id := stream()
		fe.tui.Frame()
		require.True(t, fe.viewDirty, "recalculation should be deferred")
		require.True(t, fe.recalcWakeupPending, "a wakeup should be scheduled")
		require.Nil(t, fe.rows.BySpan[id])

		// Once the last recalculation is long enough ago, the next frame
		// catches up.
		fe.lastRecalcAt = time.Now().Add(-time.Second)
		fe.Update()
		fe.tui.Frame()
		require.False(t, fe.viewDirty)
		require.NotNil(t, fe.rows.BySpan[id])

		// Explicit recalculations are never deferred.
		fe.lastRecalcCost = time.Hour
		fe.lastRecalcAt = time.Now()
		id = stream()
		fe.recalculateViewLocked()
		require.NotNil(t, fe.rows.BySpan[id])
	})
}
