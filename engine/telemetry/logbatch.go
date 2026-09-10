package telemetry

import (
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// Log-record batch settings for client DB log routes (engine/server).
//
// The SDK BatchProcessor's poll loop clones its entire batchSize-long
// []Record buffer on EVERY export tick while the exporter is ready — even
// when zero records were dequeued (sdk/log batch.go: TryDequeue's write
// callback does buf = slices.Clone(buf), and EnqueueExport returns true for
// an empty slice). A Record is a large struct (~0.5 KiB inline), so the
// resulting allocation churn is processors × ticks/sec × batchSize ×
// sizeof(Record), INDEPENDENT of actual log volume. The engine therefore uses
// one processor per session and routes each batch by stamped origin ID.
//
// Interval and batch size each scale that churn linearly. Crucially, OnEmit
// self-flushes as soon as a full batch accumulates (pollTrigger), so a longer
// interval delays only SPARSE log records — bursts still export immediately.
// 2048 queue slots keep the SDK's previous default ceiling explicit for debug
// accounting. 250ms/128 cuts the volume-independent churn ~10x while keeping
// trickle latency well below human-noticeable for log output.
const (
	LogQueueSize          = 2048
	LogExportInterval     = 250 * time.Millisecond
	LogExportMaxBatchSize = 128
)

// NewLogBatchProcessor is the log analog of NewLargeQueueLiveSpanProcessor:
// the session-owned batch processor for client DB log routes, with the bounded
// churn settings above in place of the SDK defaults.
func NewLogBatchProcessor(exp sdklog.Exporter) *sdklog.BatchProcessor {
	return sdklog.NewBatchProcessor(exp,
		sdklog.WithMaxQueueSize(LogQueueSize),
		sdklog.WithExportInterval(LogExportInterval),
		sdklog.WithExportMaxBatchSize(LogExportMaxBatchSize),
	)
}
