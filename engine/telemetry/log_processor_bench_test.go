package telemetry

import (
	"context"
	"runtime"
	"testing"
	"time"
	"unsafe"

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

type discardLogExporter struct{}

func (discardLogExporter) Export(context.Context, []sdklog.Record) error { return nil }
func (discardLogExporter) Shutdown(context.Context) error                { return nil }
func (discardLogExporter) ForceFlush(context.Context) error              { return nil }

// BenchmarkLogProcessorPoll measures what a log processor allocates per second
// of wall time, idle and with a trickle of 100 records/s, the way the CLI's
// processors sit through a long TUI session. The SDK BatchProcessor clones its
// batchSize-long []Record buffer on every poll tick, records or not, so its
// cost is ticks/s x batchSize x sizeof(Record) regardless of log volume.
func BenchmarkLogProcessorPoll(b *testing.B) {
	b.Logf("sizeof(sdklog.Record) = %d bytes", unsafe.Sizeof(sdklog.Record{}))
	for _, tc := range []struct {
		name string
		new  func(sdklog.Exporter) sdklog.Processor
	}{
		// telemetry.Init's LiveLogExporters wrapping, and the CLI's Cloud
		// forwarding processor.
		{"sdk/batch=512/interval=100ms", func(exp sdklog.Exporter) sdklog.Processor {
			return sdklog.NewBatchProcessor(exp, sdklog.WithExportInterval(100*time.Millisecond))
		}},
		// The CLI's diagnostics processor.
		{"sdk/batch=128/interval=100ms", func(exp sdklog.Exporter) sdklog.Processor {
			return sdklog.NewBatchProcessor(exp,
				sdklog.WithMaxQueueSize(LogQueueSize),
				sdklog.WithExportMaxBatchSize(LogExportMaxBatchSize),
				sdklog.WithExportInterval(100*time.Millisecond))
		}},
		// The engine's per-session processor.
		{"sdk/batch=128/interval=250ms", func(exp sdklog.Exporter) sdklog.Processor {
			return NewLogBatchProcessor(exp)
		}},
		// What the CLI uses instead of the SDK's at 512/100ms.
		{"dropping/batch=512/interval=100ms", func(exp sdklog.Exporter) sdklog.Processor {
			return NewLiveLogProcessor(exp)
		}},
	} {
		for _, rate := range []int{0, 100} {
			b.Run(tc.name+"/records="+map[int]string{0: "idle", 100: "100per_s"}[rate], func(b *testing.B) {
				ctx := context.Background()
				p := tc.new(discardLogExporter{})
				defer p.Shutdown(ctx)
				rec := blockingTestRecord(0)
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				start := time.Now()
				for b.Loop() {
					// One iteration is 100ms of wall time.
					if rate == 0 {
						time.Sleep(100 * time.Millisecond)
						continue
					}
					for range rate / 10 {
						if err := p.OnEmit(ctx, rec); err != nil {
							b.Fatal(err)
						}
						time.Sleep(time.Second / time.Duration(rate))
					}
				}
				elapsed := time.Since(start)
				runtime.ReadMemStats(&after)
				b.ReportMetric(float64(after.TotalAlloc-before.TotalAlloc)/elapsed.Seconds()/1024, "KiB/s")
			})
		}
	}
}
