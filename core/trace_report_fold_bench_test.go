package core

import (
	"flag"
	"log/slog"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/spanfootprint"
)

var reportFoldSizes = flag.String("report-fold.sizes", "", spanfootprint.SizesFlag)

var reportFoldSession *idtui.ReportSession

// BenchmarkTraceReportFold measures ReadTrace's report on the root of a
// spanfootprint session in the engine's store -- the agent-session shape
// and the nested-engine one, both with the engine's wait chains -- loading
// every span of the scope (full) or folding what the report renders nowhere
// (fold): the time to load and render, and the heap the loaded session
// retains per span of the trace.
//
//	go test ./core -run '^$' -bench TraceReportFold -benchtime 1x -report-fold.sizes 1000000
func BenchmarkTraceReportFold(b *testing.B) {
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.DiscardHandler))
	sizes, err := spanfootprint.Sizes(*reportFoldSizes)
	if err != nil {
		b.Fatal(err)
	}
	for _, shape := range []string{"agent", "nested"} {
		for _, n := range sizes {
			store, tr := footprintStore(b, n, shape == "nested")
			// the session's root, and an agent loop (an eighth of it)
			for _, scope := range []string{"root", "loop"} {
				root := tr.SpanID(0).String()
				if scope == "loop" {
					root = tr.SpanID(1).String()
				}
				for _, mode := range []string{"full", "fold"} {
					b.Run(shape+"/"+scope+"/"+mode+"/"+strconv.Itoa(n), func(b *testing.B) {
						ctx := b.Context()
						opts := readTraceReportOpts()
						var bytesPerSpan, retained float64
						var loadTime, renderTime time.Duration
						for b.Loop() {
							var before, after runtime.MemStats
							runtime.GC()
							runtime.ReadMemStats(&before)
							start := time.Now()
							var session *idtui.ReportSession
							if mode == "full" {
								session, err = loadTraceReportSessionFull(ctx, store, root)
							} else {
								session, err = loadTraceReportSession(ctx, store, root, opts)
							}
							if err != nil {
								b.Fatal(err)
							}
							loadTime = time.Since(start)
							runtime.GC()
							runtime.ReadMemStats(&after)
							bytesPerSpan = float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / float64(n)
							start = time.Now()
							if _, err := renderTraceReportSession(session, root, opts); err != nil {
								b.Fatal(err)
							}
							renderTime = time.Since(start)
							if f := session.DB().Fold(); f != nil {
								stats := f.Stats()
								retained = float64(stats.Retained) / float64(stats.Spans)
							} else {
								retained = 1
							}
							reportFoldSession = session
						}
						b.ReportMetric(bytesPerSpan, "B/span")
						b.ReportMetric(100*retained, "%retained")
						b.ReportMetric(float64(loadTime.Milliseconds()), "load-ms")
						b.ReportMetric(float64(renderTime.Milliseconds()), "render-ms")
					})
				}
			}
		}
	}
}
