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
// DB as the live CLI built it (NewDB), untracked (WithoutUpdateTracking),
// and folding with the live CLI's policy (WithFold, reading folded spans
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
		for _, mode := range []string{"full", "untracked", "fold"} {
			for _, n := range sizes {
				b.Run(shape+"/"+mode+"/"+strconv.Itoa(n), func(b *testing.B) {
					var bytesPerSpan, retained float64
					for b.Loop() {
						bytesPerSpan, retained = measureFoldFootprint(b, shape == "nested", mode, n)
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

func measureFoldFootprint(tb testing.TB, nested bool, mode string, n int) (bytesPerSpan, retained float64) {
	tr := spanfootprint.New(n)
	wrap := func(next sdktrace.SpanExporter) sdktrace.SpanExporter {
		next = tr.WaitChains(next)
		if nested {
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
	switch mode {
	case "full":
		db = NewDB()
	case "untracked":
		db = NewDB(WithoutUpdateTracking())
	case "fold":
		db = NewDB(WithFold(FoldOptions{
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
	var batches int
	if err := tr.Export(context.Background(), wrap(db), batch, func() {
		batches++
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
