package telemetry

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	otelgo "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// spanCapture records exported span snapshots, failing the first failures
// exports and blocking every export while gate is set and not yet closed.
type spanCapture struct {
	gate     chan struct{}
	failures int

	mu       sync.Mutex
	attempts int
	batches  []int
	spans    []sdktrace.ReadOnlySpan
}

func (e *spanCapture) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if e.gate != nil {
		select {
		case <-e.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attempts++
	if e.attempts <= e.failures {
		return errors.New("store unavailable")
	}
	e.batches = append(e.batches, len(spans))
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *spanCapture) Shutdown(context.Context) error { return nil }

func (e *spanCapture) snapshot() (attempts int, spans []sdktrace.ReadOnlySpan) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.attempts, append([]sdktrace.ReadOnlySpan(nil), e.spans...)
}

func callSpanStub(i int, attrs ...attribute.KeyValue) tracetest.SpanStub {
	var spanID trace.SpanID
	binary.BigEndian.PutUint64(spanID[:], uint64(i+1))
	now := time.Now()
	return tracetest.SpanStub{
		Name: "Container.withExec",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID{1},
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
		}),
		StartTime: now,
		EndTime:   now.Add(time.Millisecond),
		Attributes: append([]attribute.KeyValue{
			attribute.String(otelgo.DagDigestAttr, "xxh3:call"),
			attribute.String(otelgo.DagCallAttr, "Y2FsbA=="),
		}, attrs...),
	}
}

func TestIsCallSpan(t *testing.T) {
	t.Parallel()
	require.True(t, IsCallSpan(callSpanStub(0).Snapshot()))
	digest, ok := CallSpanDigest(callSpanStub(0).Snapshot())
	require.True(t, ok)
	require.Equal(t, "xxh3:call", digest)

	plain := callSpanStub(0)
	plain.Attributes = []attribute.KeyValue{attribute.String(otelgo.DagDigestAttr, "xxh3:call")}
	require.False(t, IsCallSpan(plain.Snapshot()), "a digest alone does not deliver a frame")
	empty := callSpanStub(0)
	empty.Attributes = []attribute.KeyValue{attribute.String(otelgo.DagCallAttr, "")}
	require.False(t, IsCallSpan(empty.Snapshot()))
}

// The protected lane exports a call span's live start snapshot as well as its
// end, while the ordinary live processor, wrapped in WithoutCallSpans, sees
// only the other spans: nothing is exported twice.
func TestCallSpanProcessorSplitsSpansWithLiveProcessor(t *testing.T) {
	t.Parallel()
	calls := &spanCapture{}
	others := &spanCapture{}
	callProc := NewCallSpanProcessor(calls)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(callProc),
		sdktrace.WithSpanProcessor(WithoutCallSpans(NewLargeQueueLiveSpanProcessor(others))),
	)
	_, callSpan := tp.Tracer("test").Start(t.Context(), "Container.withExec", trace.WithAttributes(
		attribute.String(otelgo.DagDigestAttr, "xxh3:call"),
		attribute.String(otelgo.DagCallAttr, "Y2FsbA=="),
	))
	require.NoError(t, callProc.ForceFlush(t.Context()))
	_, started := calls.snapshot()
	require.Len(t, started, 1, "the start snapshot is exported while the call runs")
	require.True(t, started[0].EndTime().Before(started[0].StartTime()), "a live snapshot")

	_, plain := tp.Tracer("test").Start(t.Context(), "plain")
	plain.End()
	callSpan.End()
	require.NoError(t, tp.ForceFlush(t.Context()))
	require.NoError(t, tp.Shutdown(t.Context()))

	_, got := calls.snapshot()
	require.Len(t, got, 2)
	require.False(t, got[1].EndTime().Before(got[1].StartTime()), "the end snapshot follows")
	for _, span := range got {
		require.Equal(t, "Container.withExec", span.Name())
	}
	_, ordinary := others.snapshot()
	require.NotEmpty(t, ordinary)
	for _, span := range ordinary {
		require.Equal(t, "plain", span.Name(), "call spans must not reach the ordinary processor")
	}
}

// Unlike the bounded live processor, the protected lane retains every call
// span while its exporter is stalled, and never blocks the emitter.
func TestCallSpanProcessorLosslessWhileExporterBlocked(t *testing.T) {
	t.Parallel()
	exp := &spanCapture{gate: make(chan struct{})}
	proc := NewCallSpanProcessor(exp)
	emitted := 2 * LargeSpanQueueSize
	for i := range emitted {
		proc.OnEnd(callSpanStub(i).Snapshot())
	}
	close(exp.gate)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	require.NoError(t, proc.Shutdown(ctx))
	_, got := exp.snapshot()
	require.Len(t, got, emitted, "no call span may be dropped on overflow")
	exp.mu.Lock()
	defer exp.mu.Unlock()
	for _, n := range exp.batches {
		require.LessOrEqual(t, n, LargeSpanExportBatchSize, "exports stay bounded")
	}
}

func TestCallSpanProcessorRetriesFailedExport(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		exp := &spanCapture{failures: 2}
		proc := NewCallSpanProcessor(exp)
		proc.OnEnd(callSpanStub(0).Snapshot())
		time.Sleep(CallPayloadExportDelay)
		synctest.Wait()
		attempts, got := exp.snapshot()
		require.Equal(t, 1, attempts)
		require.Empty(t, got)

		time.Sleep(callPayloadRetryDelay(1) + callPayloadRetryDelay(2))
		synctest.Wait()
		attempts, got = exp.snapshot()
		require.Equal(t, 3, attempts)
		require.Len(t, got, 1, "the batch is retried until it lands")
		require.NoError(t, proc.Shutdown(t.Context()))
	})
}

// A dropped batch fails the ForceFlush that saw it, once; later flushes stay
// clean, but Shutdown reports every loss of the lane's lifetime, as well as a
// span ended after it.
func TestCallSpanProcessorDropIsOneShotForFlushTerminalForShutdown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		exp := &spanCapture{failures: CallPayloadMaxExportAttempts}
		proc := NewCallSpanProcessor(exp)
		proc.OnEnd(callSpanStub(0).Snapshot())
		require.ErrorContains(t, proc.ForceFlush(t.Context()), "dropping 1 protected spans")

		proc.OnEnd(callSpanStub(1).Snapshot())
		require.NoError(t, proc.ForceFlush(t.Context()))
		require.NoError(t, proc.ForceFlush(t.Context()))
		attempts, got := exp.snapshot()
		require.Equal(t, CallPayloadMaxExportAttempts+1, attempts)
		require.Len(t, got, 1)

		require.ErrorContains(t, proc.Shutdown(t.Context()), "dropping 1 protected spans")
		proc.OnEnd(callSpanStub(2).Snapshot())
		err := proc.Shutdown(t.Context())
		require.ErrorContains(t, err, "dropping 1 protected spans")
		require.ErrorContains(t, err, "emitted after shutdown")
	})
}
