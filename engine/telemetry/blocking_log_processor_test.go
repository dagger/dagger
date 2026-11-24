package telemetry

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// gatedLogExporter records every exported body in order. Its first export
// waits for release, and it fails the first failures exports.
type gatedLogExporter struct {
	release  chan struct{}
	entered  chan struct{}
	once     sync.Once
	mu       sync.Mutex
	bodies   []string
	failures int
	calls    int
}

func (e *gatedLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.once.Do(func() {
		close(e.entered)
		select {
		case <-e.release:
		case <-ctx.Done():
		}
	})
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.calls <= e.failures {
		return errors.New("injected export failure")
	}
	for _, rec := range records {
		e.bodies = append(e.bodies, rec.Body().AsString())
	}
	return nil
}
func (*gatedLogExporter) Shutdown(context.Context) error   { return nil }
func (*gatedLogExporter) ForceFlush(context.Context) error { return nil }

func blockingTestRecord(i int) *sdklog.Record {
	var rec sdklog.Record
	rec.SetBody(log.StringValue(strconv.Itoa(i)))
	return &rec
}

// A burst far larger than the queue, against an exporter that is stalled and
// then fails twice, arrives whole and in order: the producer waits instead of
// losing records.
func TestBlockingLogProcessorLosesNothing(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	exporter := &gatedLogExporter{release: make(chan struct{}), entered: make(chan struct{}), failures: 2}
	p := NewBlockingLogProcessor(exporter, 8, 4, time.Millisecond)

	const burst = 500
	produced := make(chan error, 1)
	go func() {
		for i := range burst {
			if err := p.OnEmit(ctx, blockingTestRecord(i)); err != nil {
				produced <- err
				return
			}
		}
		produced <- nil
	}()
	select {
	case <-exporter.entered:
	case <-ctx.Done():
		t.Fatal("no export started")
	}
	close(exporter.release)
	require.NoError(t, <-produced)
	require.NoError(t, p.ForceFlush(ctx))
	require.NoError(t, p.Shutdown(ctx))

	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	require.Len(t, exporter.bodies, burst)
	for i, body := range exporter.bodies {
		require.Equal(t, strconv.Itoa(i), body, "records arrive in order, once each")
	}
}

// Against a stalled exporter, a DroppingLogProcessor never makes the producer
// wait: a full queue loses its oldest records, like the SDK BatchProcessor's.
// A failed export is not retried.
func TestDroppingLogProcessorDropsOldest(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	exporter := &gatedLogExporter{release: make(chan struct{}), entered: make(chan struct{}), failures: 1}
	p := NewDroppingLogProcessor(exporter, 4, 2, time.Millisecond)

	// The first batch is stuck in the export, which then fails.
	require.NoError(t, p.OnEmit(ctx, blockingTestRecord(0)))
	require.NoError(t, p.OnEmit(ctx, blockingTestRecord(1)))
	select {
	case <-exporter.entered:
	case <-ctx.Done():
		t.Fatal("no export started")
	}
	for i := 2; i < 100; i++ {
		require.NoError(t, p.OnEmit(ctx, blockingTestRecord(i)))
	}
	close(exporter.release)
	require.NoError(t, p.ForceFlush(ctx))
	require.NoError(t, p.Shutdown(ctx))

	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	require.Equal(t, []string{"96", "97", "98", "99"}, exporter.bodies)
}

// NewLiveLogProcessor takes the SDK BatchProcessor's OTEL_BLRP_* settings,
// as the SDK processor telemetry.Init used to wrap live exporters in did.
func TestLiveLogProcessorHonorsBLRPEnv(t *testing.T) {
	for _, key := range []string{"OTEL_BLRP_MAX_QUEUE_SIZE", "OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "OTEL_BLRP_EXPORT_TIMEOUT"} {
		t.Setenv(key, "")
	}
	p := NewLiveLogProcessor(failingLogExporter{})
	require.Equal(t, LogQueueSize, p.limit)
	require.Equal(t, 512, p.batchSize)
	require.Equal(t, 30*time.Second, p.exportTimeout)
	require.NoError(t, p.Shutdown(t.Context()))

	t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "4")
	t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "8")
	t.Setenv("OTEL_BLRP_EXPORT_TIMEOUT", "1500")
	p = NewLiveLogProcessor(failingLogExporter{})
	require.Equal(t, 4, p.limit)
	require.Equal(t, 4, p.batchSize, "clamped to the queue size, like the SDK")
	require.Equal(t, 1500*time.Millisecond, p.exportTimeout)
	require.NoError(t, p.Shutdown(t.Context()))
}

type failingLogExporter struct{}

func (failingLogExporter) Export(context.Context, []sdklog.Record) error {
	return errors.New("cloud unreachable")
}
func (failingLogExporter) Shutdown(context.Context) error   { return nil }
func (failingLogExporter) ForceFlush(context.Context) error { return nil }

// With an exporter that never succeeds, Shutdown still returns at its bound
// and releases a producer waiting for queue space.
func TestBlockingLogProcessorShutdownIsBounded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	p := NewBlockingLogProcessor(failingLogExporter{}, 1, 1, 0)
	require.NoError(t, p.OnEmit(ctx, blockingTestRecord(0)))
	waiting := make(chan error, 1)
	go func() {
		// Fills the queue while the first record is being retried, then waits.
		err := p.OnEmit(ctx, blockingTestRecord(1))
		if err == nil {
			err = p.OnEmit(ctx, blockingTestRecord(2))
		}
		waiting <- err
	}()

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer shutdownCancel()
	start := time.Now()
	require.ErrorIs(t, p.Shutdown(shutdownCtx), context.DeadlineExceeded)
	require.Less(t, time.Since(start), 10*time.Second)
	require.NoError(t, <-waiting, "a waiting producer is released by Shutdown")
}
