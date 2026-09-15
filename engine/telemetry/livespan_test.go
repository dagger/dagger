package telemetry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// gatedExporter blocks every ExportSpans call until released, simulating a span
// consumer (the per-client SQLite writer, or the Cloud exporter) that has
// stalled or fallen behind.
type gatedExporter struct {
	gate chan struct{}

	mu       sync.Mutex
	exported int
}

var _ sdktrace.SpanExporter = (*gatedExporter)(nil)

func (e *gatedExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	select {
	case <-e.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	e.mu.Lock()
	e.exported += len(spans)
	e.mu.Unlock()
	return nil
}

func (e *gatedExporter) Shutdown(context.Context) error { return nil }

func (e *gatedExporter) exportedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exported
}

// TestLiveSpanProcessorBoundedNonBlocking proves the two properties the
// live-span hop is sized for: with a completely stalled consumer, (1) emitting
// far more spans than the queue holds never blocks the emitting goroutine, and
// (2) the number of span snapshots the processor retains for later export is
// bounded by the queue (plus at most one in-flight batch) — the overflow is
// dropped, not buffered. The retained count is the processor's memory ceiling:
// each retained snapshot is a full span copy held in heap until the consumer
// drains it.
func TestLiveSpanProcessorBoundedNonBlocking(t *testing.T) {
	t.Parallel()

	// A batch larger than the queue would be pointless and would inflate the
	// preallocated per-processor batch slice; guard the relationship.
	require.LessOrEqual(t, LargeSpanExportBatchSize, LargeSpanQueueSize)

	exp := &gatedExporter{gate: make(chan struct{})}
	proc := NewLargeQueueLiveSpanProcessor(exp)

	// The batch processor only enqueues sampled spans.
	snap := tracetest.SpanStub{
		Name: "test-span",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID{1},
			SpanID:     trace.SpanID{1},
			TraceFlags: trace.FlagsSampled,
		}),
		StartTime: time.Now(),
		EndTime:   time.Now(),
	}.Snapshot()

	// Emit 2x the queue capacity while the exporter is fully stalled.
	emitted := 2 * LargeSpanQueueSize
	start := time.Now()
	for range emitted {
		proc.OnEnd(snap)
	}
	elapsed := time.Since(start)

	// Property 1: emission is non-blocking even with a stalled consumer. The
	// loop is pure channel sends/drops (~ms); the generous bound only guards
	// against a regression to blocking behavior.
	require.Less(t, elapsed, 30*time.Second,
		"span emission must not block on a stalled exporter")

	// Release the consumer and drain everything that was retained.
	close(exp.gate)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, proc.Shutdown(ctx))

	got := exp.exportedCount()

	// Property 2: retention is bounded. Everything that survives to the
	// consumer was held in memory while the consumer was stalled, so the
	// exported total is exactly the processor's worst-case snapshot retention:
	// at most the full queue plus one in-flight batch (with one batch of
	// slack for scheduling variance), and never the unbounded emitted total.
	require.Less(t, got, emitted,
		"overflow must be dropped, not retained")
	require.LessOrEqual(t, got, LargeSpanQueueSize+2*LargeSpanExportBatchSize,
		"retained snapshots must be bounded by queue + in-flight batch")

	// And the queue genuinely buffers up to its capacity (the burst-headroom
	// half of the trade): nothing below the full queue was dropped.
	require.GreaterOrEqual(t, got, LargeSpanQueueSize,
		"spans within queue capacity must be retained, not dropped")
}
