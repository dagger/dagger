package dagui

import (
	"context"
	"runtime"
	"strconv"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/internal/spanfootprint"
)

// BenchmarkFoldFootprint is BenchmarkSpanFootprint over the agent-session
// trace with the engine's wait chains, and in the nested-engine shape, for a
// DB keeping every span (NewDB, the default), the same tracking updates for a
// remote frontend (WithUpdateTracking, as dagger.io's per-viewer DBs do), and
// folding with the live CLI's policy (WithFold, reading folded spans
// back from a store standing in for the engine's, outside the heap
// measured). It reports the DB's retained heap per span and, folding, the
// share of the spans it retains:
//
//	go test ./dagql/dagui -run '^$' -bench FoldFootprint -benchtime 1x -footprint.sizes 1000000
func BenchmarkFoldFootprint(b *testing.B) {
	sizes, err := spanfootprint.Sizes(*footprintSizes)
	if err != nil {
		b.Fatal(err)
	}
	for _, shape := range []string{"agent", "nested"} {
		for _, mode := range []string{"tracked", "full", "fold"} {
			for _, n := range sizes {
				b.Run(shape+"/"+mode+"/"+strconv.Itoa(n), func(b *testing.B) {
					var bytesPerSpan, retained float64
					for b.Loop() {
						bytesPerSpan, retained = measureFoldFootprint(b, footprintCase{nested: shape == "nested", mode: mode, n: n})
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

// BenchmarkFoldFootprintVariants is BenchmarkFoldFootprint over variants of
// the trace and policy: in "progress", one span in fifty reports transfers
// (spanfootprint Trace.Progress), as pulls and uploads do;
// "progress-mayrollup" is the same with progress spans retained only when
// they may roll up, as RetainRendered did before it retained them beneath
// boundaries too; in "idargs", a fifth of the calls take an earlier call's
// ID as an argument (spanfootprint Trace.IDArgs):
//
//	go test ./dagql/dagui -run '^$' -bench FoldFootprintVariants -benchtime 1x -footprint.sizes 1000000
func BenchmarkFoldFootprintVariants(b *testing.B) {
	sizes, err := spanfootprint.Sizes(*footprintSizes)
	if err != nil {
		b.Fatal(err)
	}
	for _, shape := range []string{"agent", "nested"} {
		for _, variant := range []string{"base", "progress", "progress-mayrollup", "idargs", "idargs+progress"} {
			for _, mode := range []string{"full", "fold"} {
				for _, n := range sizes {
					c := footprintCase{nested: shape == "nested", mode: mode, n: n}
					switch variant {
					case "progress":
						c.progress = true
					case "progress-mayrollup":
						if mode == "full" {
							continue
						}
						c.progress, c.retain = true, retainProgressMayRollUp
					case "idargs":
						c.idArgs = true
					case "idargs+progress":
						c.idArgs, c.progress = true, true
					}
					b.Run(shape+"/"+variant+"/"+mode+"/"+strconv.Itoa(n), func(b *testing.B) {
						var bytesPerSpan, retained float64
						for b.Loop() {
							bytesPerSpan, retained = measureFoldFootprint(b, c)
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
}

// retainProgressMayRollUp is RetainRendered as it was before progress spans
// were retained wherever they sit.
func retainProgressMayRollUp(span FoldSpan) bool {
	if span.Agent() || span.CauseLinks() || span.SurfacedKind() {
		return true
	}
	if !span.Failed() && !span.Reveal() && !span.Progress() {
		return false
	}
	return span.MayRollUp()
}

// footprintCase is one measurement of measureFoldFootprint.
type footprintCase struct {
	nested bool
	// mode is tracked, full (the default DB) or fold
	mode string
	n    int
	// progress reports the trace's transfers (Trace.Progress) as its
	// spans start and end; idArgs gives calls ID arguments (Trace.IDArgs).
	progress, idArgs bool
	// retain is the fold policy (nil: RetainRendered).
	retain FoldRetain
}

func measureFoldFootprint(tb testing.TB, c footprintCase) (bytesPerSpan, retained float64) {
	n := c.n
	tr := spanfootprint.New(n)
	tr.IDArgs = c.idArgs
	wrap := func(next sdktrace.SpanExporter) sdktrace.SpanExporter {
		next = tr.WaitChains(next)
		if c.nested {
			next = spanfootprint.Nested(next)
		}
		return next
	}
	const batch = 512
	// what's ended so far: the store's spans, regenerated on demand
	endedBelow, allEnded := spanfootprint.LongLived, false
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var db *DB
	switch c.mode {
	case "tracked":
		db = NewDB(WithUpdateTracking())
	case "full":
		db = NewDB()
	case "fold":
		db = NewDB(WithFold(FoldOptions{
			Retain: c.retain,
			Load: func(ids []SpanID) {
				exp := wrap(db)
				for _, id := range ids {
					i := spanfootprint.SpanIndex(id.SpanID)
					done := allEnded || (i >= spanfootprint.LongLived && i < endedBelow)
					if err := exp.ExportSpans(FoldFill(context.Background()), []sdktrace.ReadOnlySpan{tr.Stub(i, done)}); err != nil {
						tb.Fatal(err)
					}
				}
			},
		}))
	}
	// progress: halfway as a span starts, done as it ends
	report := func(lo, hi int, done bool) {
		if !c.progress {
			return
		}
		for i := lo; i < hi; i++ {
			for _, item := range tr.Progress(i) {
				current := item.Total / 2
				if done {
					current = item.Total
				}
				db.IngestProgress(SpanID{tr.SpanID(i)}, item.Name, current, item.Total, "bytes")
			}
		}
	}
	var batches int
	if err := tr.Export(context.Background(), wrap(db), batch, func() {
		batches++
		lo := (batches - 1) * batch
		report(lo, min(lo+batch, n), false)
		report(max(lo-batch, spanfootprint.LongLived), min(lo, n), true)
		endedBelow = max((batches-1)*batch, spanfootprint.LongLived)
		if batches == (n+batch)/batch {
			allEnded = true
		}
	}); err != nil {
		tb.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	footprintDB = db
	bytesPerSpan = float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / float64(n)
	if db.fold != nil {
		stats := db.fold.Stats()
		if stats.Spans != n {
			tb.Fatalf("received %d spans, want %d", stats.Spans, n)
		}
		retained = float64(stats.Retained) / float64(n)
	} else if len(db.Spans.Order) != n {
		tb.Fatalf("ingested %d spans, want %d", len(db.Spans.Order), n)
	}
	return bytesPerSpan, retained
}
