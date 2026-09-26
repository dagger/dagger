package telemetry

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"unsafe"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	otelgo "github.com/dagger/otel-go"

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
			scope: "test.core",
			body:  logapi.BytesValue([]byte("payload")),
			attrs: []logapi.KeyValue{logapi.String(otelgo.ContentTypeAttr, telemetryattrs.CallPayloadContentType)},
			want:  true,
		},
		{
			name:  "other content type",
			scope: "test.core",
			body:  logapi.BytesValue([]byte("payload")),
			attrs: []logapi.KeyValue{logapi.String(otelgo.ContentTypeAttr, "application/json")},
		},
		{
			name:  "wrong content type kind",
			scope: "test.core",
			body:  logapi.BytesValue([]byte("payload")),
			attrs: []logapi.KeyValue{logapi.Bool(otelgo.ContentTypeAttr, true)},
		},
		{
			name:  "missing content type",
			scope: "test.core",
			body:  logapi.BytesValue([]byte("payload")),
		},
		{
			name:  "wrong body kind",
			scope: "test.core",
			body:  logapi.StringValue("payload"),
			attrs: []logapi.KeyValue{logapi.String(otelgo.ContentTypeAttr, telemetryattrs.CallPayloadContentType)},
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

	// Keep the coalescing timer from firing partway through emission when
	// the host is loaded: this test asserts batching of a fully queued closure.
	synctest.Test(t, testCallPayloadBatchProcessorFiltersAndDrainsQueue)
}

func testCallPayloadBatchProcessorFiltersAndDrainsQueue(t *testing.T) {
	exp := &countingLogExporter{}
	proc := NewCallPayloadBatchProcessor(exp)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))

	var ordinary sdklog.Record
	ordinary.SetBody(logapi.StringValue("ordinary"))
	require.NoError(t, proc.OnEmit(t.Context(), &ordinary))

	var payload logapi.Record
	payload.SetBody(logapi.BytesValue([]byte("payload")))
	payload.AddAttributes(logapi.String(otelgo.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	logger := provider.Logger("test.core")
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

func TestWithoutCallPayloadsKeepsPayloadsOffOrdinaryPath(t *testing.T) {
	t.Parallel()

	ordinaryExp := &countingLogExporter{}
	payloadExp := &countingLogExporter{}
	payloadProc := NewCallPayloadBatchProcessor(payloadExp)
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(payloadProc),
		sdklog.WithProcessor(WithoutCallPayloads(sdklog.NewSimpleProcessor(ordinaryExp))),
	)
	logger := provider.Logger("test.core")

	var payload logapi.Record
	payload.SetBody(logapi.BytesValue([]byte("payload")))
	payload.AddAttributes(logapi.String(otelgo.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	var ordinary logapi.Record
	ordinary.SetBody(logapi.StringValue("exec output"))
	for range 3 {
		logger.Emit(t.Context(), payload)
		logger.Emit(t.Context(), ordinary)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, provider.ForceFlush(ctx))
	require.Equal(t, 3, ordinaryExp.count(),
		"ordinary logs must still reach the ordinary processor")
	require.Equal(t, 3, payloadExp.count(),
		"payloads must be exported exactly once, by the payload processor")
	require.NoError(t, provider.Shutdown(ctx))
}

// flakyLogExporter fails the first failures exports, then records every
// record body it is handed in order.
type flakyLogExporter struct {
	mu       sync.Mutex
	failures int
	attempts int
	bodies   []string
	batches  []int
}

func (e *flakyLogExporter) Export(_ context.Context, recs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attempts++
	if e.attempts <= e.failures {
		return errors.New("client db unavailable")
	}
	e.batches = append(e.batches, len(recs))
	for _, rec := range recs {
		e.bodies = append(e.bodies, string(rec.Body().AsBytes()))
	}
	return nil
}

func (e *flakyLogExporter) stats() (int, []string, []int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.attempts, append([]string(nil), e.bodies...), append([]int(nil), e.batches...)
}

func (*flakyLogExporter) Shutdown(context.Context) error   { return nil }
func (*flakyLogExporter) ForceFlush(context.Context) error { return nil }

func payloadRecordWithBody(body string) logapi.Record {
	var rec logapi.Record
	rec.SetBody(logapi.BytesValue([]byte(body)))
	rec.AddAttributes(logapi.String(otelgo.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	return rec
}

// The payload processor is the only transport for payload records, so a
// failed export must be retried by the processor itself — in order, so a
// root never lands after the dependencies emitted behind it — with records
// emitted during the backoff queued behind the failed batch.
func TestCallPayloadBatchProcessorRetriesFailedBatchInOrder(t *testing.T) {
	t.Parallel()

	// Batch boundaries and retry attempts must not depend on wall-clock
	// pauses while the initial records are being emitted.
	synctest.Test(t, testCallPayloadBatchProcessorRetriesFailedBatchInOrder)
}

func testCallPayloadBatchProcessorRetriesFailedBatchInOrder(t *testing.T) {
	exp := &flakyLogExporter{failures: 2}
	proc := NewCallPayloadBatchProcessor(exp)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))
	logger := provider.Logger("test.core")

	const first = LogExportMaxBatchSize + 1
	for i := range first {
		logger.Emit(t.Context(), payloadRecordWithBody(strconv.Itoa(i)))
	}
	require.Eventually(t, func() bool {
		attempts, _, _ := exp.stats()
		return attempts >= 1
	}, time.Second, time.Millisecond, "the first batch must reach the exporter")
	// Emitted while the failed batch is waiting for its retry.
	logger.Emit(t.Context(), payloadRecordWithBody("late"))

	require.Eventually(t, func() bool {
		_, bodies, _ := exp.stats()
		return len(bodies) == first+1
	}, 5*time.Second, 5*time.Millisecond, "every record must land once the exporter recovers")

	attempts, bodies, batches := exp.stats()
	require.Equal(t, 2+2, attempts, "two failed attempts, then the two batches in one drain")
	want := make([]string, 0, first+1)
	for i := range first {
		want = append(want, strconv.Itoa(i))
	}
	want = append(want, "late")
	require.Equal(t, want, bodies, "retries must preserve emission order and never duplicate")
	require.Equal(t, []int{LogExportMaxBatchSize, 2}, batches)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, proc.Shutdown(ctx))
}

// Canceling a flush must not cancel the processor's background retry schedule.
func TestCallPayloadBatchProcessorRetriesAfterCanceledFlush(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		exp := &flakyLogExporter{failures: 2}
		proc := NewCallPayloadBatchProcessor(exp)
		provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))
		defer func() { require.NoError(t, proc.Shutdown(t.Context())) }()
		logger := provider.Logger("test.core")
		logger.Emit(t.Context(), payloadRecordWithBody("first"))
		time.Sleep(CallPayloadExportDelay)
		synctest.Wait()
		attempts, _, _ := exp.stats()
		require.Equal(t, 1, attempts)

		ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
		defer cancel()
		require.ErrorIs(t, proc.ForceFlush(ctx), context.DeadlineExceeded)
		synctest.Wait()
		// The exporter recovers now. A new record must also eventually land,
		// even though it joins the nonempty queue left by the canceled flush.
		logger.Emit(t.Context(), payloadRecordWithBody("second"))
		time.Sleep(CallPayloadExportDelay)
		synctest.Wait()
		attempts, _, _ = exp.stats()
		require.Equal(t, 2, attempts, "cancellation must retain the retry backoff")
		time.Sleep(callPayloadRetryDelay(2))
		synctest.Wait()
		attempts, bodies, _ := exp.stats()
		require.Equal(t, 3, attempts)
		require.Equal(t, []string{"first", "second"}, bodies)
	})
}

// A batch that never lands must eventually be dropped rather than wedge the
// queue. The drop is reported with the records' digests: the session exporter
// released their delivery claims, but only a walk that reaches them through a
// still-undelivered root would re-emit them.
func TestCallPayloadBatchProcessorDropsBatchAfterMaxAttempts(t *testing.T) {
	t.Parallel()

	exp := &flakyLogExporter{failures: CallPayloadMaxExportAttempts}
	proc := NewCallPayloadBatchProcessor(exp)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))
	logger := provider.Logger("test.core")
	logger.Emit(t.Context(), payloadRecordWithBody("doomed"))

	// A drain retries to completion and reports the drop it caused.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	err := proc.ForceFlush(ctx)
	require.ErrorContains(t, err, "dropping 1 protected records")
	attempts, bodies, _ := exp.stats()
	require.Equal(t, CallPayloadMaxExportAttempts, attempts)
	require.Empty(t, bodies)

	// The queue is clear: a later record exports on the first try, and that
	// flush reports only its own pass — an earlier drop must not fail every
	// later in-session flush.
	logger.Emit(t.Context(), payloadRecordWithBody("repaired"))
	require.NoError(t, proc.ForceFlush(ctx))
	_, bodies, _ = exp.stats()
	require.Equal(t, []string{"repaired"}, bodies)
	// Shutdown still reports the loss, exactly once, for the session seal.
	err = proc.Shutdown(ctx)
	require.ErrorContains(t, err, "dropping 1 protected records")
	require.Equal(t, 1, strings.Count(err.Error(), "dropping"))
}

// captureLogExporter keeps every record it is handed.
type captureLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *captureLogExporter) Export(_ context.Context, recs []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, recs...)
	return nil
}

func (*captureLogExporter) Shutdown(context.Context) error   { return nil }
func (*captureLogExporter) ForceFlush(context.Context) error { return nil }

func TestCallPayloadDigestsForDropReport(t *testing.T) {
	t.Parallel()

	stamped := payloadRecordWithBody("stamped")
	stamped.AddAttributes(logapi.String(telemetryattrs.CallPayloadDigestAttr, "xxh3:abc"))
	capture := &captureLogExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(capture)))
	logger := provider.Logger("test.core")
	logger.Emit(t.Context(), stamped)
	logger.Emit(t.Context(), payloadRecordWithBody("unstamped"))
	require.NoError(t, provider.Shutdown(t.Context()))
	require.Equal(t, []string{"xxh3:abc", "?"}, callPayloadDigests(capture.records))
}

func TestCallPayloadRetryDelayBackoff(t *testing.T) {
	t.Parallel()

	require.Equal(t, CallPayloadRetryBaseDelay, callPayloadRetryDelay(1))
	require.Equal(t, 2*CallPayloadRetryBaseDelay, callPayloadRetryDelay(2))
	require.Equal(t, 4*CallPayloadRetryBaseDelay, callPayloadRetryDelay(3))
	require.Equal(t, CallPayloadRetryMaxDelay, callPayloadRetryDelay(CallPayloadMaxExportAttempts))
	require.Equal(t, CallPayloadRetryMaxDelay, callPayloadRetryDelay(100))
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

// Records that arrive while a pass is exporting must wait out the coalescing
// delay like a fresh burst rather than being drained immediately, so a
// sustained trickle produces closure-sized batches instead of one Export per
// handful of records.
func TestCallPayloadBatchProcessorRecoalescesBetweenPasses(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		exp := &blockingLogExporter{started: make(chan struct{}), release: make(chan struct{})}
		proc := NewCallPayloadBatchProcessor(exp)
		provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))
		logger := provider.Logger("test.core")

		logger.Emit(t.Context(), payloadRecordWithBody("first"))
		<-exp.started
		// Arrives while the first pass is inside the exporter.
		logger.Emit(t.Context(), payloadRecordWithBody("second"))
		close(exp.release)
		synctest.Wait()
		// Arrives inside the re-armed coalescing window.
		logger.Emit(t.Context(), payloadRecordWithBody("third"))
		synctest.Wait()
		_, batches := exp.stats()
		require.Equal(t, []int{1}, batches, "records arriving mid-pass must wait out the coalescing delay")

		time.Sleep(CallPayloadExportDelay)
		synctest.Wait()
		_, batches = exp.stats()
		require.Equal(t, []int{1, 2}, batches, "the trickle must export as one coalesced batch")

		require.NoError(t, proc.Shutdown(t.Context()))
	})
}

func TestCallPayloadBatchProcessorLosslessWhileExporterBlocked(t *testing.T) {
	t.Parallel()

	exp := &blockingLogExporter{started: make(chan struct{}), release: make(chan struct{})}
	proc := NewCallPayloadBatchProcessor(exp)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))
	logger := provider.Logger("test.core")

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
		payloads[i].AddAttributes(logapi.String(otelgo.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
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

// Shutdown retries a failed batch after its backoff for as long as its
// context allows, rather than giving up after one failed pass, and returns
// with the worker stopped.
func TestCallPayloadBatchProcessorShutdownRetriesWithinItsContext(t *testing.T) {
	t.Parallel()

	exp := &flakyLogExporter{failures: 2}
	proc := NewCallPayloadBatchProcessor(exp)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))
	provider.Logger("test.core").Emit(t.Context(), payloadRecordWithBody("kept"))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, proc.Shutdown(ctx))
	attempts, bodies, _ := exp.stats()
	require.Equal(t, []string{"kept"}, bodies, "the payload lands on a retry within the shutdown's time")
	require.Equal(t, 3, attempts)
	select {
	case <-proc.done:
	default:
		t.Fatal("Shutdown returned before the worker stopped")
	}
}

// ctxBlockingLogExporter holds every export until its context ends and
// counts the exports in flight.
type ctxBlockingLogExporter struct {
	entered  chan struct{}
	once     sync.Once
	inFlight atomic.Int32
}

func (e *ctxBlockingLogExporter) Export(ctx context.Context, _ []sdklog.Record) error {
	e.inFlight.Add(1)
	defer e.inFlight.Add(-1)
	e.once.Do(func() { close(e.entered) })
	<-ctx.Done()
	return ctx.Err()
}

func (*ctxBlockingLogExporter) Shutdown(context.Context) error   { return nil }
func (*ctxBlockingLogExporter) ForceFlush(context.Context) error { return nil }

// An export the worker started on its own, still running when Shutdown's
// time ends, is cancelled, and Shutdown returns only once the worker stopped,
// so no export outlives it.
func TestCallPayloadBatchProcessorShutdownEndsActiveExport(t *testing.T) {
	t.Parallel()

	exp := &ctxBlockingLogExporter{entered: make(chan struct{})}
	proc := NewCallPayloadBatchProcessor(exp)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))
	provider.Logger("test.core").Emit(t.Context(), payloadRecordWithBody("stuck"))
	select {
	case <-exp.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not start its export")
	}

	const bound = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), bound)
	defer cancel()
	start := time.Now()
	require.ErrorIs(t, proc.Shutdown(ctx), context.DeadlineExceeded)
	require.Less(t, time.Since(start), bound+time.Second)
	select {
	case <-proc.done:
	default:
		t.Fatal("Shutdown returned before the worker stopped")
	}
	require.Zero(t, exp.inFlight.Load(), "no export outlives Shutdown")
}

// An export started by ForceFlush, whose context outlives Shutdown's, ends
// when Shutdown's time ends too: Shutdown returns on its deadline, with the
// worker stopped and no export in flight.
func TestCallPayloadBatchProcessorShutdownEndsFlushExport(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		exp := &ctxBlockingLogExporter{entered: make(chan struct{})}
		proc := NewCallPayloadBatchProcessor(exp)
		provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(proc))
		provider.Logger("test.core").Emit(t.Context(), payloadRecordWithBody("flushed"))

		// Time stands still until every goroutine blocks, so the flush
		// reaches the worker before its coalescing timer fires.
		flushCtx, cancelFlush := context.WithTimeout(t.Context(), time.Hour)
		defer cancelFlush()
		flushed := make(chan error, 1)
		go func() { flushed <- proc.ForceFlush(flushCtx) }()
		<-exp.entered

		const bound = 200 * time.Millisecond
		ctx, cancel := context.WithTimeout(t.Context(), bound)
		defer cancel()
		start := time.Now()
		require.ErrorIs(t, proc.Shutdown(ctx), context.DeadlineExceeded)
		require.Equal(t, bound, time.Since(start), "Shutdown returns on its own deadline")
		select {
		case <-proc.done:
		default:
			t.Fatal("Shutdown returned before the worker stopped")
		}
		require.Zero(t, exp.inFlight.Load(), "no export outlives Shutdown")
		require.ErrorIs(t, <-flushed, context.Canceled, "the flush's export was cancelled")
	})
}
