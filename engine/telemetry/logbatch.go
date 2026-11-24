package telemetry

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	telemetry "github.com/dagger/otel-go"
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

// NewLiveLogProcessor is what telemetry.Init would wrap a LiveLogExporter in
// — exporting every telemetry.NearlyImmediate, with the SDK's queue size,
// batch size and export timeout, from OTEL_BLRP_MAX_QUEUE_SIZE,
// OTEL_BLRP_MAX_EXPORT_BATCH_SIZE and OTEL_BLRP_EXPORT_TIMEOUT like the SDK —
// as a DroppingLogProcessor, which costs nothing while no logs arrive. For
// long-lived processes such as the CLI.
func NewLiveLogProcessor(exp sdklog.Exporter) *DroppingLogProcessor {
	queueSize := blrpEnv("OTEL_BLRP_MAX_QUEUE_SIZE", LogQueueSize)
	batchSize := min(blrpEnv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", liveLogExportMaxBatchSize), queueSize)
	timeout := time.Duration(blrpEnv("OTEL_BLRP_EXPORT_TIMEOUT", int(droppingLogExportTimeout/time.Millisecond))) * time.Millisecond
	return newDroppingLogProcessor(exp, queueSize, batchSize, telemetry.NearlyImmediate, timeout)
}

const liveLogExportMaxBatchSize = 512

// blrpEnv reads a positive integer OTEL_BLRP_* setting the way the SDK's
// BatchProcessor does, or returns fallback.
func blrpEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		otel.Handle(fmt.Errorf("invalid %s value %s: %w", key, v, err))
		return fallback
	}
	if n < 1 {
		return fallback
	}
	return n
}

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
