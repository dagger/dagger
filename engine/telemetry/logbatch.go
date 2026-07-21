package telemetry

import (
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// Log-record batch settings for the per-client DB log routes (engine/server).
//
// The SDK BatchProcessor's poll loop clones its entire batchSize-long
// []Record buffer on EVERY export tick while the exporter is ready — even
// when zero records were dequeued (sdk/log batch.go: TryDequeue's write
// callback does buf = slices.Clone(buf), and EnqueueExport returns true for
// an empty slice). A Record is a large struct (~0.5 KiB inline), so the
// resulting allocation churn is processors × ticks/sec × batchSize ×
// sizeof(Record), INDEPENDENT of actual log volume. The engine registers one
// processor per client per ancestor route, so a grouped session easily holds
// 100+ of them; at the previous 100ms interval and default 512 batch this
// produced tens of GB of allocation churn per run (measured: 22–37 GB of
// slices.Clone[[]sdklog.Record] in a grouped python-sdk check run).
//
// Interval and batch size each scale that churn linearly. Crucially, OnEmit
// self-flushes as soon as a full batch accumulates (pollTrigger), so a longer
// interval delays only SPARSE log records — bursts still export immediately.
// 250ms/128 cuts the volume-independent churn ~10x while keeping trickle
// latency well below human-noticeable for log output.
const (
	LogExportInterval     = 250 * time.Millisecond
	LogExportMaxBatchSize = 128
)

// NewLogBatchProcessor is the log analog of NewLargeQueueLiveSpanProcessor:
// the batch processor for every per-client DB log route, with the bounded
// churn settings above in place of the SDK defaults.
func NewLogBatchProcessor(exp sdklog.Exporter) *sdklog.BatchProcessor {
	return sdklog.NewBatchProcessor(exp,
		sdklog.WithExportInterval(LogExportInterval),
		sdklog.WithExportMaxBatchSize(LogExportMaxBatchSize),
	)
}
