package dagui

import (
	"context"
	"flag"
	"runtime"
	"strconv"
	"testing"
	"unsafe"

	"github.com/dagger/dagger/internal/spanfootprint"
)

type footprint struct {
	spans          int
	bytesPerSpan   float64
	objectsPerSpan float64
}

var footprintDB *DB

var footprintSizes = flag.String("footprint.sizes", "", spanfootprint.SizesFlag)

// measureFootprint ingests an n-span spanfootprint.Trace into a fresh DB and
// reports the live heap the DB holds per span, measured after a GC.
func measureFootprint(tb testing.TB, n int) footprint {
	tb.Helper()
	tr := spanfootprint.New(n)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	db := NewDB()
	if err := tr.Export(context.Background(), db, 512, nil); err != nil {
		tb.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if len(db.Spans.Order) != n {
		tb.Fatalf("ingested %d spans, want %d", len(db.Spans.Order), n)
	}
	// Keep the DB reachable past the benchmark, so -memprofile's inuse
	// samples show what it retains.
	footprintDB = db
	return footprint{
		spans:          n,
		bytesPerSpan:   float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / float64(n),
		objectsPerSpan: float64(int64(after.HeapObjects)-int64(before.HeapObjects)) / float64(n),
	}
}

// BenchmarkSpanFootprint reports the DB's retained heap per span after
// ingesting a large synthetic agent-session trace:
//
//	go test ./dagql/dagui -run '^$' -bench SpanFootprint -benchtime 1x
//
// For a breakdown, add -memprofile mem.out -memprofilerate 512 and read it
// with go tool pprof -sample_index=inuse_space. Pass -footprint.sizes (e.g.
// 100000,1000000) to measure other sizes.
func BenchmarkSpanFootprint(b *testing.B) {
	sizes, err := spanfootprint.Sizes(*footprintSizes)
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range sizes {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			var fp footprint
			for b.Loop() {
				fp = measureFootprint(b, n)
			}
			b.ReportMetric(fp.bytesPerSpan, "B/span")
			b.ReportMetric(fp.objectsPerSpan, "objs/span")
			// Span's own size, for spotting it crossing a size class
			// (1024 bytes, with the 8-byte malloc header of pointerful
			// objects over 512, holds up to 1016).
			b.ReportMetric(float64(unsafe.Sizeof(Span{})), "B/Span-struct")
		})
	}
}

// TestFootprintTrace keeps the footprint generator honest: the trace it
// produces ingests into a coherent tree with the span kinds it claims.
func TestFootprintTrace(t *testing.T) {
	const n = 5_000
	tr := spanfootprint.New(n)
	db := NewDB()
	if err := tr.Export(context.Background(), db, 512, nil); err != nil {
		t.Fatal(err)
	}
	if len(db.Spans.Order) != n {
		t.Fatalf("ingested %d spans, want %d", len(db.Spans.Order), n)
	}
	var calls, payloads, llm, tests, failed, running int
	for _, span := range db.Spans.Order {
		if span.CallDigest != "" {
			calls++
		}
		if span.Call() != nil {
			payloads++
		}
		if span.LLMRole != "" {
			llm++
		}
		if span.TestCaseName != "" {
			tests++
		}
		if span.IsFailed() {
			failed++
		}
		if span.IsRunning() {
			running++
		}
	}
	if calls == 0 || payloads == 0 || llm == 0 || tests == 0 || failed == 0 {
		t.Fatalf("missing span kinds: calls=%d payloads=%d llm=%d tests=%d failed=%d",
			calls, payloads, llm, tests, failed)
	}
	if running != 0 {
		t.Fatalf("%d spans still running after the trace completed", running)
	}
	if db.RootSpan == nil || db.RootSpan.ChildSpans.Len() == 0 {
		t.Fatal("root span has no children")
	}
}
