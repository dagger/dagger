package idtui

import (
	"context"
	"io"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/vito/tuist"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/spanfootprint"
)

// BenchmarkFrontendFoldFootprint is BenchmarkFrontendSpanFootprint over
// the agent-session trace with the engine's wait chains, and in the
// nested-engine shape, for the live CLI's DB as it was (dagui.NewDB()),
// untracked (dagui.WithoutUpdateTracking) and folding (newFoldingDB, reading
// folded spans back from a store standing in for the engine's, outside the
// heap measured). It reports the frontend's retained heap per span and, when
// folding, the share of spans it retains:
//
//	go test ./dagql/idtui -run '^$' -bench FrontendFoldFootprint -benchtime 1x -footprint.sizes 1000000
func BenchmarkFrontendFoldFootprint(b *testing.B) {
	sizes, err := spanfootprint.Sizes(*footprintSizes)
	if err != nil {
		b.Fatal(err)
	}
	for _, shape := range []string{"agent", "nested"} {
		for _, mode := range []string{"full", "untracked", "fold"} {
			for _, n := range sizes {
				b.Run(shape+"/"+mode+"/"+strconv.Itoa(n), func(b *testing.B) {
					var bytesPerSpan, retained float64
					for b.Loop() {
						bytesPerSpan, retained = foldFootprint(b, shape, mode, n)
					}
					b.ReportMetric(bytesPerSpan, "B/span")
					if mode == "fold" {
						b.ReportMetric(100*retained, "%retained")
					}
				})
			}
		}
	}
}

func foldFootprint(b testing.TB, shape, mode string, n int) (bytesPerSpan, retained float64) {
	tr := spanfootprint.New(n)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fe := streamFootprint(b, tr, shape, mode)
	runtime.GC()
	runtime.ReadMemStats(&after)
	footprintFrontend = fe
	bytesPerSpan = float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / float64(n)
	if f := fe.db.Fold(); f != nil {
		stats := f.Stats()
		if stats.Spans != n {
			b.Fatalf("received %d spans, want %d", stats.Spans, n)
		}
		retained = float64(stats.Retained) / float64(stats.Spans)
	} else if got := len(fe.db.Spans.Order); got != n {
		b.Fatalf("ingested %d spans, want %d", got, n)
	}
	return bytesPerSpan, retained
}

// streamFootprint streams tr, with the engine's wait chains (in the nested
// shape, if asked), into a live TUI frontend whose DB is as mode says,
// rendering a frame every few batches, and lets what it reads back land.
func streamFootprint(b testing.TB, tr *spanfootprint.Trace, shape, mode string) *frontendPretty {
	n := tr.N
	wrap := func(next sdktrace.SpanExporter) sdktrace.SpanExporter {
		next = tr.WaitChains(next)
		if shape == "nested" {
			next = spanfootprint.Nested(next)
		}
		return next
	}
	// the store: what's ended so far, regenerated on demand
	var endedBelow, allEnded atomic.Int64
	endedBelow.Store(spanfootprint.LongLived)
	query := func(_ context.Context, ids []dagui.SpanID) ([]sdktrace.ReadOnlySpan, error) {
		var out captureSpans
		exp := wrap(&out)
		for _, id := range ids {
			i := spanfootprint.SpanIndex(id.SpanID)
			if i < 0 || i >= n {
				continue
			}
			done := allEnded.Load() != 0 || (i >= spanfootprint.LongLived && int64(i) < endedBelow.Load())
			if err := exp.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{tr.Stub(i, done)}); err != nil {
				return nil, err
			}
		}
		return []sdktrace.ReadOnlySpan(out), nil
	}

	var db *dagui.DB
	var lf *liveFold
	switch mode {
	case "full":
		db = dagui.NewDB()
	case "untracked":
		db = newLocalDB()
	case "fold":
		db, lf = newFoldingDB()
	}
	fe := newWithTerminal(io.Discard, db, tuist.NewHeadlessTerminal(120, 40))
	if lf != nil {
		lf.attach(fe)
		fe.SetSpanQuery(dagui.TraceID{TraceID: tr.Stub(0, false).SpanContext().TraceID()}, query)
	}
	fe.SetPrimary(dagui.SpanID{SpanID: tr.SpanID(0)})
	const batch = 512
	var batches int
	if err := tr.Export(context.Background(), wrap(fe.SpanExporter()), batch, func() {
		batches++
		endedBelow.Store(int64(max((batches-1)*batch, spanfootprint.LongLived)))
		if batches%4 == 0 {
			fe.tui.Step()
		}
	}); err != nil {
		b.Fatal(err)
	}
	allEnded.Store(1)
	fe.tui.Step()
	if lf != nil {
		// let the reads land
		for range 64 {
			lf.wait(context.Background())
			fe.tui.Step()
			lf.mu.Lock()
			idle := lf.inflight == 0
			lf.mu.Unlock()
			if idle {
				break
			}
		}
	}
	fe.tui.Step()
	return fe
}

type captureSpans []sdktrace.ReadOnlySpan

func (c *captureSpans) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	*c = append(*c, spans...)
	return nil
}

func (c *captureSpans) Shutdown(context.Context) error { return nil }
