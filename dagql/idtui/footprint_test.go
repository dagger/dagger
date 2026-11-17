package idtui

import (
	"context"
	"io"
	"runtime"
	"strconv"
	"testing"

	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/internal/spanfootprint"
)

var footprintFrontend *frontendPretty

// BenchmarkFrontendSpanFootprint reports the live pretty frontend's retained
// heap per span (its DB included) after a large synthetic agent-session
// trace streams through its span exporter, rendering a frame every few
// batches as the live TUI does:
//
//	go test ./dagql/idtui -run '^$' -bench FrontendSpanFootprint -benchtime 1x
//
// Compare dagui's BenchmarkSpanFootprint for the DB alone. Set
// DAGGER_SPAN_FOOTPRINT_SIZES (e.g. "100000,1000000") to measure other sizes.
func BenchmarkFrontendSpanFootprint(b *testing.B) {
	sizes, err := spanfootprint.Sizes()
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range sizes {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			var bytesPerSpan, objsPerSpan float64
			for b.Loop() {
				tr := spanfootprint.New(n)
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				fe := newWithTerminal(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(120, 40))
				fe.SetPrimary(dagui.SpanID{SpanID: tr.SpanID(0)})
				var batches int
				if err := tr.Export(context.Background(), fe.SpanExporter(), 512, func() {
					batches++
					if batches%4 == 0 {
						fe.tui.Step()
					}
				}); err != nil {
					b.Fatal(err)
				}
				fe.tui.Step()
				runtime.GC()
				runtime.ReadMemStats(&after)
				if got := len(fe.db.Spans.Order); got != n {
					b.Fatalf("ingested %d spans, want %d", got, n)
				}
				footprintFrontend = fe
				bytesPerSpan = float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / float64(n)
				objsPerSpan = float64(int64(after.HeapObjects)-int64(before.HeapObjects)) / float64(n)
			}
			b.ReportMetric(bytesPerSpan, "B/span")
			b.ReportMetric(objsPerSpan, "objs/span")
		})
	}
}
