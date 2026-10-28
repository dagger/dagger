package telemetry

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/slog"
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
	span := tracetest.SpanStub{
		Name: "test-span",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID{1},
			SpanID:     trace.SpanID{1},
			TraceFlags: trace.FlagsSampled,
		}),
		StartTime: time.Now(),
		EndTime:   time.Now(),
	}

	// Emit 2x the queue capacity while the exporter is fully stalled.
	emitted := 2 * LargeSpanQueueSize
	start := time.Now()
	for i := range emitted {
		// Use distinct spans so export coalescing doesn't affect queue accounting.
		var spanID trace.SpanID
		binary.BigEndian.PutUint64(spanID[:], uint64(i+1))
		span.SpanContext = span.SpanContext.WithSpanID(spanID)
		proc.OnEnd(span.Snapshot())
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

// The bounded lane counts what it drops: with the consumer stalled through a
// burst, the count is exactly the spans emitted that the consumer never got,
// and without overflow it is zero.
func TestSpanQueueCountsDrops(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		emitted int
	}{
		{name: "overflow", emitted: 2 * LargeSpanQueueSize},
		{name: "within capacity", emitted: LargeSpanQueueSize / 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exp := &gatedExporter{gate: make(chan struct{})}
			var counts SpanQueueCounts
			proc := NewCountedLargeQueueLiveSpanProcessor(exp, &counts)
			span := tracetest.SpanStub{
				Name: "test-span",
				SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
					TraceID:    trace.TraceID{1},
					TraceFlags: trace.FlagsSampled,
				}),
				StartTime: time.Now(),
				EndTime:   time.Now(),
			}
			for i := range tc.emitted {
				// distinct spans, so export coalescing keeps them all
				var spanID trace.SpanID
				binary.BigEndian.PutUint64(spanID[:], uint64(i+1))
				span.SpanContext = span.SpanContext.WithSpanID(spanID)
				proc.OnEnd(span.Snapshot())
			}
			// The batch processor never queues an unsampled span, so it
			// isn't one the lane dropped.
			unsampled := span
			unsampled.SpanContext = unsampled.SpanContext.WithTraceFlags(0)
			proc.OnEnd(unsampled.Snapshot())

			close(exp.gate)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			require.NoError(t, proc.Shutdown(ctx))

			got := exp.exportedCount()
			require.Equal(t, int64(tc.emitted-got), counts.Dropped())
			require.Equal(t, int64(tc.emitted), counts.Queued())
			if tc.emitted > LargeSpanQueueSize {
				require.Positive(t, counts.Dropped())
			} else {
				require.Zero(t, counts.Dropped())
			}
		})
	}
}

// A shutdown cut short by its deadline leaves spans the queue still hands on
// afterwards: the report must not call them dropped, only once the queue has
// drained may it give a final count.
func TestSpanQueueCountsShutdownCutShort(t *testing.T) {
	t.Parallel()
	exp := &gatedExporter{gate: make(chan struct{})}
	var counts SpanQueueCounts
	proc := NewCountedLargeQueueLiveSpanProcessor(exp, &counts)
	span := tracetest.SpanStub{
		Name: "test-span",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID{1},
			TraceFlags: trace.FlagsSampled,
		}),
		StartTime: time.Now(),
		EndTime:   time.Now(),
	}
	const emitted = 100 // well within the queue's capacity: nothing overflows
	for i := range emitted {
		var spanID trace.SpanID
		binary.BigEndian.PutUint64(spanID[:], uint64(i+1))
		span.SpanContext = span.SpanContext.WithSpanID(spanID)
		proc.OnEnd(span.Snapshot())
	}

	// The exporter is stalled, so the shutdown runs out of time.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	shutdownErr := proc.Shutdown(ctx)
	require.Error(t, shutdownErr)
	var out bytes.Buffer
	counts.LogShutdown(slog.New(slog.NewTextHandler(&out, nil)), shutdownErr)
	require.Contains(t, out.String(), `msg="session telemetry span count incomplete"`)
	require.NotContains(t, out.String(), "dropped")

	// The queue still hands its spans on once the exporter resumes: none of
	// them was dropped.
	close(exp.gate)
	require.Eventually(t, func() bool { return exp.exportedCount() == emitted }, 30*time.Second, 10*time.Millisecond)
	require.Zero(t, counts.Dropped())
	out.Reset()
	counts.LogShutdown(slog.New(slog.NewTextHandler(&out, nil)), nil)
	require.Contains(t, out.String(), `msg="session telemetry dropped spans"`)
	require.Contains(t, out.String(), "dropped=0")
}
