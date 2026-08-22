package telemetry

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/stretchr/testify/require"
	logapi "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"google.golang.org/protobuf/proto"
)

// countingLogExporter records how many log records arrive and when the first
// batch lands.
type countingLogExporter struct {
	mu        sync.Mutex
	exported  int
	batches   int
	flushes   int
	shutdowns int
	firstAt   time.Time
}

var _ sdklog.Exporter = (*countingLogExporter)(nil)

func (e *countingLogExporter) Export(_ context.Context, recs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.exported == 0 && len(recs) > 0 {
		e.firstAt = time.Now()
	}
	if len(recs) > 0 {
		e.batches++
	}
	e.exported += len(recs)
	return nil
}

func (e *countingLogExporter) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.shutdowns++
	return nil
}

func (e *countingLogExporter) ForceFlush(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.flushes++
	return nil
}

func (e *countingLogExporter) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exported
}

func (e *countingLogExporter) batchCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.batches
}

func (e *countingLogExporter) lifecycleCounts() (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.flushes, e.shutdowns
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

func TestCallPayloadBatchProcessorFastPathRecordShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope string
		body  logapi.Value
		attrs []logapi.KeyValue
		want  bool
	}{
		{
			name:  "valid raw payload",
			scope: telemetryattrs.CallPayloadInstrumentationScope,
			body:  logapi.BytesValue([]byte("payload")),
			attrs: []logapi.KeyValue{logapi.Bool(telemetryattrs.DagCallPayloadAttr, true)},
			want:  true,
		},
		{
			name:  "false marker",
			scope: telemetryattrs.CallPayloadInstrumentationScope,
			body:  logapi.BytesValue([]byte("payload")),
			attrs: []logapi.KeyValue{logapi.Bool(telemetryattrs.DagCallPayloadAttr, false)},
		},
		{
			name:  "wrong marker type",
			scope: telemetryattrs.CallPayloadInstrumentationScope,
			body:  logapi.BytesValue([]byte("payload")),
			attrs: []logapi.KeyValue{logapi.String(telemetryattrs.DagCallPayloadAttr, "true")},
		},
		{
			name:  "missing marker",
			scope: telemetryattrs.CallPayloadInstrumentationScope,
			body:  logapi.BytesValue([]byte("payload")),
		},
		{
			name:  "wrong scope",
			scope: "wrong.scope",
			body:  logapi.BytesValue([]byte("payload")),
			attrs: []logapi.KeyValue{logapi.Bool(telemetryattrs.DagCallPayloadAttr, true)},
		},
		{
			name:  "wrong body kind",
			scope: telemetryattrs.CallPayloadInstrumentationScope,
			body:  logapi.StringValue("payload"),
			attrs: []logapi.KeyValue{logapi.Bool(telemetryattrs.DagCallPayloadAttr, true)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exp := &countingLogExporter{}
			proc := NewCallPayloadBatchProcessor(exp)
			provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))

			var record logapi.Record
			record.SetBody(test.body)
			record.AddAttributes(test.attrs...)
			provider.Logger(test.scope).Emit(t.Context(), record)

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			require.NoError(t, proc.ForceFlush(ctx))
			if test.want {
				require.Equal(t, 1, exp.count())
			} else {
				require.Zero(t, exp.count(), "malformed reserved record must remain on the normal batch path")
			}
			require.NoError(t, proc.Shutdown(ctx))
		})
	}
}

func TestCallPayloadBatchProcessorFiltersAndDrainsQueue(t *testing.T) {
	t.Parallel()

	exp := &countingLogExporter{}
	proc := NewCallPayloadBatchProcessor(exp)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))

	var ordinary sdklog.Record
	ordinary.SetBody(logapi.StringValue("ordinary"))
	require.NoError(t, proc.OnEmit(t.Context(), &ordinary))

	var payload logapi.Record
	payload.SetBody(logapi.BytesValue([]byte("payload")))
	payload.AddAttributes(logapi.Bool(telemetryattrs.DagCallPayloadAttr, true))
	logger := provider.Logger(telemetryattrs.CallPayloadInstrumentationScope)
	const records = 2*LogExportMaxBatchSize + 1
	for range records {
		logger.Emit(t.Context(), payload)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, proc.ForceFlush(ctx))
	require.Equal(t, records, exp.count())
	require.Equal(t, 3, exp.batchCount(), "the full queued closure must drain in back-to-back bounded batches")

	// The worker has no idle poll loop, so draining leaves no delayed empty or
	// duplicate exports behind.
	time.Sleep(2 * CallPayloadExportDelay)
	require.Equal(t, records, exp.count())

	require.NoError(t, proc.Shutdown(ctx))
	flushes, shutdowns := exp.lifecycleCounts()
	require.Zero(t, flushes, "the payload processor does not own the shared exporter")
	require.Zero(t, shutdowns, "the payload processor does not own the shared exporter")
}

type blockingLogExporter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once

	mu       sync.Mutex
	exported int
	batches  []int
}

func (e *blockingLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.once.Do(func() { close(e.started) })
	<-e.release
	e.mu.Lock()
	defer e.mu.Unlock()
	e.exported += len(records)
	e.batches = append(e.batches, len(records))
	return nil
}

func (e *blockingLogExporter) stats() (int, []int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exported, append([]int(nil), e.batches...)
}

func (*blockingLogExporter) Shutdown(context.Context) error   { return nil }
func (*blockingLogExporter) ForceFlush(context.Context) error { return nil }

func TestCallPayloadBatchProcessorLosslessWhileExporterBlocked(t *testing.T) {
	t.Parallel()

	exp := &blockingLogExporter{started: make(chan struct{}), release: make(chan struct{})}
	proc := NewCallPayloadBatchProcessor(exp)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))
	logger := provider.Logger(telemetryattrs.CallPayloadInstrumentationScope)

	// Build distinct, valid raw call payloads so the burst models a real recipe
	// closure rather than duplicate log traffic.
	const records = 2*LogQueueSize + 1
	payloads := make([]logapi.Record, records+1)
	for i := range payloads {
		body, err := proto.Marshal(&callpbv1.Call{
			Field: "dependency",
			Type:  &callpbv1.Type{NamedType: "Thing"},
			Args: []*callpbv1.Argument{{
				Name: "index",
				Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{
					String_: strconv.Itoa(i),
				}},
			}},
		})
		require.NoError(t, err)
		payloads[i].SetBody(logapi.BytesValue(body))
		payloads[i].AddAttributes(logapi.Bool(telemetryattrs.DagCallPayloadAttr, true))
	}
	logger.Emit(t.Context(), payloads[0])

	select {
	case <-exp.started:
	case <-time.After(time.Second):
		t.Fatal("payload batch did not reach exporter")
	}

	// Hold the exporter while enqueueing more than the old 2048-record cap.
	// Agent recipes can be tens of thousands of calls; dropping the tail from
	// both this fast path and the ordinary bounded log processor left the root
	// claimed while nested ID arguments such as Directory.withChanges(changes:)
	// never reached the spawning client. OnEmit must stay nonblocking, but this
	// dedicated immutable-payload queue must retain the entire burst.
	returned := make(chan struct{})
	go func() {
		for i := 1; i < len(payloads); i++ {
			logger.Emit(context.Background(), payloads[i])
		}
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("OnEmit blocked behind the exporter")
	}

	close(exp.release)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, proc.ForceFlush(ctx))
	exported, batches := exp.stats()
	require.Equal(t, records+1, exported,
		"every dependency in an oversized call-payload closure must reach the exporter")
	for _, size := range batches {
		require.LessOrEqual(t, size, LogExportMaxBatchSize,
			"lossless ingress must still use bounded exporter batches")
	}
	require.NoError(t, proc.Shutdown(ctx))
}
