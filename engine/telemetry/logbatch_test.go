package telemetry

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	logapi "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// countingLogExporter records how many log records arrive and when the first
// batch lands.
type countingLogExporter struct {
	mu       sync.Mutex
	exported int
	firstAt  time.Time
}

var _ sdklog.Exporter = (*countingLogExporter)(nil)

func (e *countingLogExporter) Export(_ context.Context, recs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.exported == 0 && len(recs) > 0 {
		e.firstAt = time.Now()
	}
	e.exported += len(recs)
	return nil
}

func (e *countingLogExporter) Shutdown(context.Context) error   { return nil }
func (e *countingLogExporter) ForceFlush(context.Context) error { return nil }

func (e *countingLogExporter) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exported
}

// TestLogBatchProcessorIdleChurnBounded guards the property that motivated
// NewLogBatchProcessor: the SDK poll loop clones its full batchSize-long
// []Record buffer on every ready tick even with zero log traffic, so idle
// allocation churn is processors x ticks x batchSize x sizeof(Record). The
// test runs idle processors for a fixed window and asserts total allocation
// stays under a bound derived from the configured interval and batch size —
// reverting to the previous settings (100ms interval, 512 batch: 10x the
// churn) blows the bound.
//
// Deliberately NOT parallel: it reads runtime-global allocation counters.
func TestLogBatchProcessorIdleChurnBounded(t *testing.T) {
	const (
		procs  = 16
		window = 2 * time.Second
	)

	exps := make([]*countingLogExporter, procs)
	bsps := make([]*sdklog.BatchProcessor, procs)
	for i := range procs {
		exps[i] = &countingLogExporter{}
		bsps[i] = NewLogBatchProcessor(exps[i])
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, p := range bsps {
			require.NoError(t, p.Shutdown(ctx))
		}
	}()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	time.Sleep(window)
	runtime.ReadMemStats(&after)

	allocated := after.TotalAlloc - before.TotalAlloc

	// Expected idle churn: one full-buffer clone per processor per tick.
	//
	// The interval/batch factors below are deliberately literal copies of the
	// intended settings, NOT the exported constants: the bound is a pinned
	// budget. If someone reverts the constants toward the old 100ms/512
	// settings (10x the churn), the processors allocate against THIS budget
	// and the test fails; deriving the bound from the constants themselves
	// would let it self-scale and guard nothing.
	recSize := uint64(unsafe.Sizeof(sdklog.Record{}))
	ticks := uint64(window/(250*time.Millisecond)) + 2 // +2 slack for timer skew
	perClone := uint64(128) * recSize
	// 3x margin for runtime noise (timers, GC bookkeeping, test harness).
	bound := 3 * procs * ticks * perClone

	require.Less(t, allocated, bound,
		"idle log batch processors allocated %d bytes in %s; bound %d "+
			"(churn must stay proportional to interval x batch size)",
		allocated, window, bound)

	// Sanity: idle means nothing was actually exported.
	for _, e := range exps {
		require.Zero(t, e.count())
	}
}

// TestLogBatchProcessorBurstSelfFlush proves the property that makes the
// longer export interval safe: OnEmit triggers an immediate export the moment
// a full batch accumulates, so bursts do not wait for the interval tick.
func TestLogBatchProcessorBurstSelfFlush(t *testing.T) {
	t.Parallel()

	exp := &countingLogExporter{}
	proc := NewLogBatchProcessor(exp)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		require.NoError(t, proc.Shutdown(ctx))
	}()

	var rec sdklog.Record
	rec.SetTimestamp(time.Now())
	rec.SetBody(logapi.StringValue("burst"))

	start := time.Now()
	for range 2 * LogExportMaxBatchSize {
		require.NoError(t, proc.OnEmit(context.Background(), &rec))
	}

	// A full batch must reach the exporter well before the export interval
	// elapses; use half the interval as the deadline to prove the self-flush
	// path (not the ticker) delivered it.
	require.Eventually(t, func() bool {
		return exp.count() >= LogExportMaxBatchSize
	}, LogExportInterval/2, time.Millisecond,
		"a full batch must self-flush immediately, not wait for the ticker")
	require.Less(t, time.Since(start), LogExportInterval,
		"burst delivery must not depend on the export interval")
}

// TestLogBatchProcessorTrickleDelivery proves sparse records still arrive
// within roughly one export interval (the latency cost of the churn fix is
// bounded and small).
func TestLogBatchProcessorTrickleDelivery(t *testing.T) {
	t.Parallel()

	exp := &countingLogExporter{}
	proc := NewLogBatchProcessor(exp)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		require.NoError(t, proc.Shutdown(ctx))
	}()

	var rec sdklog.Record
	rec.SetTimestamp(time.Now())
	rec.SetBody(logapi.StringValue("trickle"))
	require.NoError(t, proc.OnEmit(context.Background(), &rec))

	require.Eventually(t, func() bool { return exp.count() >= 1 },
		2*LogExportInterval, 5*time.Millisecond,
		"a single sparse record must arrive within ~one export interval")
}
