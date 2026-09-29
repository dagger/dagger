package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	otelgo "github.com/dagger/otel-go"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

const (
	// CallPayloadExportDelay is how long the first payload of a burst waits
	// for the rest of its recipe closure before the batch is exported.
	CallPayloadExportDelay = 5 * time.Millisecond

	// CallPayloadRetryBaseDelay is the backoff after the first failed export
	// of a payload batch; it doubles per consecutive failure up to
	// CallPayloadRetryMaxDelay.
	CallPayloadRetryBaseDelay = 50 * time.Millisecond
	CallPayloadRetryMaxDelay  = 2 * time.Second
	// CallPayloadMaxExportAttempts bounds how often one batch is retried
	// before it is dropped, so a dead client DB cannot wedge the queue
	// forever. Dropping is lossy: the session exporter releases the failed
	// targets, but a later closure walk only re-emits a dropped record if it
	// reaches it through a root that is itself still undelivered. When the
	// root already landed, every walk from it short-circuits at the root's
	// claim and the dropped dependency stays missing for that client unless
	// some other chain happens to include it. The drop is logged with the
	// records' digests so that gap is at least diagnosable.
	CallPayloadMaxExportAttempts = 8
)

// CallPayloadBatchProcessor gives immutable call payloads a short, on-demand
// path to the session exporter while the ordinary log processor retains its
// lower-frequency batching. OnEmit only clones and enqueues matching records;
// one worker wakes on the first payload, briefly coalesces its recipe closure,
// then drains it in bounded exporter batches. The ingress queue is deliberately
// lossless and therefore unbounded: dropping any one record can leave a client
// with a call whose nested recipe dependency can never be rebuilt, while the
// root's per-target claim suppresses every later closure walk that could repair
// it. Recipe bursts are finite and deduplicated per delivery target; bounding
// exporter batches, rather than ingress, keeps each persistence operation
// bounded.
//
// This is the ONLY log transport for payload records (WithoutCallPayloads
// keeps them out of the ordinary processor), so it owns their retries too: a
// batch whose export fails goes back to the head of the queue, in order, and
// is retried with exponential backoff until it lands or
// CallPayloadMaxExportAttempts is spent. Shutdown keeps retrying within its
// context; when that ends, it cancels the export in flight and returns only
// once the worker has stopped, so the caller may shut the exporter down.
type CallPayloadBatchProcessor struct {
	*protectedBatcher[sdklog.Record]
	accept func(sdklog.Record) bool
}

func NewCallPayloadBatchProcessor(exporter sdklog.Exporter) *CallPayloadBatchProcessor {
	return newProtectedBatchProcessor(exporter, IsCallPayloadRecord)
}

// NewControlBatchProcessor protects revisioned agent and subscription records
// from the ordinary bounded queue. Capture success is not a delivery receipt:
// ForceFlush returns the persistence failures of its own pass, and Shutdown
// returns every terminal persistence failure of the processor's lifetime.
func NewControlBatchProcessor(exporter sdklog.Exporter) *CallPayloadBatchProcessor {
	return newProtectedBatchProcessor(exporter, agentcontrol.IsRecord)
}

func newProtectedBatchProcessor(exporter sdklog.Exporter, accept func(sdklog.Record) bool) *CallPayloadBatchProcessor {
	return &CallPayloadBatchProcessor{
		protectedBatcher: newProtectedBatcher(protectedBatcherConfig[sdklog.Record]{
			export:    exporter.Export,
			describe:  callPayloadDigests,
			batchSize: LogExportMaxBatchSize,
			kind:      "records",
		}),
		accept: accept,
	}
}

func (processor *CallPayloadBatchProcessor) OnEmit(_ context.Context, record *sdklog.Record) error {
	if record == nil || !processor.accept(*record) {
		return nil
	}
	return processor.enqueue(record.Clone())
}

func (processor *CallPayloadBatchProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool {
	return true
}

// protectedBatcher is the lossless, retrying worker behind every protected
// telemetry lane: call payload and agent control log records
// (CallPayloadBatchProcessor) and call spans (CallSpanProcessor). Ingress
// never blocks and never drops; one worker coalesces a burst for
// CallPayloadExportDelay, exports it in bounded batches, and retries a failed
// batch in order with exponential backoff until it lands or
// CallPayloadMaxExportAttempts is spent.
type protectedBatcher[T any] struct {
	export    func(context.Context, []T) error
	describe  func([]T) []string
	batchSize int
	kind      string
	// terminalErr accumulates every loss (dropped batches, items enqueued
	// after shutdown) for Shutdown, so the session-end seal learns of it even
	// when the loss happened long before. ForceFlush deliberately reports only
	// its own pass: a single in-session drop must not fail every later flush.
	terminalErr error

	// ctx ends the worker's own exports (the coalesced and retried ones);
	// Shutdown cancels it when its own context ends.
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	queue    []T
	failures int // consecutive failed exports of the batch at the queue head
	stopped  bool

	wake     chan struct{}
	flush    chan protectedBatchRequest
	shutdown chan protectedBatchRequest
	done     chan struct{}
}

type protectedBatcherConfig[T any] struct {
	// export delivers one bounded batch; an error retries it.
	export func(context.Context, []T) error
	// describe names a dropped batch's items for diagnostics.
	describe func([]T) []string
	// batchSize bounds each export.
	batchSize int
	// kind names the items in errors, e.g. "records" or "spans".
	kind string
}

type protectedBatchRequest struct {
	ctx  context.Context
	done chan error
}

func newProtectedBatcher[T any](config protectedBatcherConfig[T]) *protectedBatcher[T] {
	ctx, cancel := context.WithCancel(context.Background())
	batcher := &protectedBatcher[T]{
		export:    config.export,
		describe:  config.describe,
		batchSize: config.batchSize,
		kind:      config.kind,
		ctx:       ctx,
		cancel:    cancel,
		queue:     make([]T, 0, config.batchSize),
		wake:      make(chan struct{}, 1),
		flush:     make(chan protectedBatchRequest),
		shutdown:  make(chan protectedBatchRequest, 1),
		done:      make(chan struct{}),
	}
	go batcher.run()
	return batcher
}

// enqueue appends an item the caller already owns (a clone or immutable
// snapshot) and wakes the worker. After Shutdown it records the loss and
// returns the terminal error instead.
func (batcher *protectedBatcher[T]) enqueue(item T) error {
	batcher.mu.Lock()
	if batcher.stopped {
		batcher.terminalErr = errors.Join(batcher.terminalErr, fmt.Errorf("protected %s emitted after shutdown", batcher.kind))
		err := batcher.terminalErr
		batcher.mu.Unlock()
		return err
	}
	wake := len(batcher.queue) == 0
	batcher.queue = append(batcher.queue, item)
	batcher.mu.Unlock()

	if wake {
		select {
		case batcher.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

func (batcher *protectedBatcher[T]) ForceFlush(ctx context.Context) error {
	batcher.mu.Lock()
	stopped := batcher.stopped
	batcher.mu.Unlock()
	if stopped {
		select {
		case <-batcher.done:
			return batcher.deliveryError()
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}

	request := protectedBatchRequest{ctx: ctx, done: make(chan error, 1)}
	select {
	case batcher.flush <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-batcher.done:
		return batcher.deliveryError()
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-batcher.done:
		return batcher.deliveryError()
	}
}

func (batcher *protectedBatcher[T]) Shutdown(ctx context.Context) error {
	batcher.mu.Lock()
	if batcher.stopped {
		batcher.mu.Unlock()
		select {
		case <-batcher.done:
			return batcher.deliveryError()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	batcher.stopped = true
	batcher.mu.Unlock()

	request := protectedBatchRequest{ctx: ctx, done: make(chan error, 1)}
	batcher.shutdown <- request
	select {
	case err := <-request.done:
		// The worker returns right after reporting, but its deferred cleanup
		// (and close(done)) still runs after the send. Wait for it, so
		// Shutdown keeps its promise that the worker has stopped.
		<-batcher.done
		return err
	case <-ctx.Done():
		// Out of time: end the export in flight and wait for the worker, so
		// no export outlives Shutdown.
		batcher.cancel()
		<-batcher.done
		return ctx.Err()
	}
}

// run is the single worker. Between exports it sleeps on the wake signal; a
// wake arms the coalescing delay, a failed export arms the retry backoff
// instead, and an explicit flush or shutdown exports immediately either way.
func (batcher *protectedBatcher[T]) run() {
	defer close(batcher.done)
	var timer *time.Timer
	var timerC <-chan time.Time
	arm := func(delay time.Duration) {
		if timer != nil {
			stopTimer(timer)
		}
		timer = time.NewTimer(delay)
		timerC = timer.C
	}
	disarm := func() {
		if timer != nil {
			stopTimer(timer)
			timer = nil
		}
		timerC = nil
	}
	defer disarm()
	// export runs exportPass over the queue. A failed batch arms its retry
	// backoff. Items that arrived during a pass are coalesced like a fresh
	// burst — the delay is re-armed rather than draining them at once, so a
	// sustained trickle still exports in closure-sized batches instead of one
	// Export per handful of items — unless drain is set, in which case
	// passes continue until the queue is observed empty.
	export := func(ctx context.Context, drain bool) error {
		disarm()
		// Only this call's outcome: drops from earlier passes stay in
		// terminalErr for Shutdown rather than failing every later flush.
		var errs error
		for {
			retryIn, more, dropped, failed := batcher.exportPass(ctx)
			errs = errors.Join(errs, dropped)
			err := errors.Join(errs, failed)
			switch {
			case retryIn > 0:
				if drain {
					timer := time.NewTimer(retryIn)
					select {
					case <-timer.C:
						continue
					case <-ctx.Done():
						stopTimer(timer)
						// Only this drain was canceled. The failed batch is
						// still queued, so resume its background retries.
						arm(retryIn)
						return errors.Join(err, context.Cause(ctx))
					}
				}
				arm(retryIn)
				return err
			case !more:
				return err
			case !drain:
				arm(CallPayloadExportDelay)
				return err
			}
		}
	}
	for {
		select {
		case <-batcher.wake:
			if timerC == nil {
				arm(CallPayloadExportDelay)
			}
			// A pending retry backoff keeps its schedule; the new items
			// queue up behind the failed batch and export with it.
		case <-timerC:
			if err := export(batcher.ctx, false); err != nil {
				otel.Handle(err)
			}
		case request := <-batcher.flush:
			// A flush's export ends with the flush, or when Shutdown cancels
			// the worker, whichever comes first.
			ctx, stop := batcher.withWorker(request.ctx)
			request.done <- export(ctx, true)
			stop()
		case request := <-batcher.shutdown:
			disarm()
			// Every loss of the batcher's lifetime (drops during the drain
			// included) is already in terminalErr; add what the drain left
			// undelivered, and keep the result terminal for later calls.
			interrupted := batcher.drain(request.ctx)
			batcher.mu.Lock()
			batcher.terminalErr = errors.Join(batcher.terminalErr, interrupted)
			err := batcher.terminalErr
			batcher.mu.Unlock()
			request.done <- err
			return
		}
	}
}

// withWorker returns ctx, also cancelled when Shutdown cancels the worker.
func (batcher *protectedBatcher[T]) withWorker(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	stopAfter := context.AfterFunc(batcher.ctx, cancel)
	return ctx, func() {
		stopAfter()
		cancel()
	}
}

// drain exports everything queued for Shutdown, retrying a failed batch
// after its backoff for as long as ctx allows; a batch that spends
// CallPayloadMaxExportAttempts is dropped as usual, which exportPass records
// in terminalErr. It reports what ctx cut short: the last failure still
// queued for retry, and ctx's error — not failures a retry repaired.
func (batcher *protectedBatcher[T]) drain(ctx context.Context) error {
	defer batcher.cancel()
	var retrying error
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(retrying, err)
		}
		retryIn, more, _, failed := batcher.exportPass(ctx)
		if retryIn > 0 {
			retrying = failed
			retry := time.NewTimer(retryIn)
			select {
			case <-retry.C:
				continue
			case <-ctx.Done():
				stopTimer(retry)
				return errors.Join(retrying, ctx.Err())
			}
		}
		retrying = nil
		if !more {
			return nil
		}
	}
}

// exportPass takes everything currently queued and exports it in bounded
// batches, in order. On a failed export the unexported tail (failed batch
// first) goes back to the head of the queue, retryIn says how long to back
// off before trying again, and failed is that export's error; 0 means the pass
// completed, with any batch that exceeded its attempts given up on. dropped
// reports this pass's give-ups (each also accumulated into terminalErr for
// Shutdown). more reports whether items arrived while the pass ran and are
// now queued.
func (batcher *protectedBatcher[T]) exportPass(ctx context.Context) (retryIn time.Duration, more bool, dropped, failed error) {
	batcher.mu.Lock()
	queued := batcher.queue
	batcher.queue = nil
	failures := batcher.failures
	batcher.mu.Unlock()

	for len(queued) > 0 {
		batchSize := min(len(queued), batcher.batchSize)
		batch := queued[:batchSize]
		exportErr := batcher.export(ctx, batch)
		if exportErr == nil {
			failures = 0
			clear(batch)
			queued = queued[batchSize:]
			continue
		}
		failures++
		if failures < CallPayloadMaxExportAttempts {
			batcher.mu.Lock()
			batcher.queue = append(queued, batcher.queue...)
			batcher.failures = failures
			batcher.mu.Unlock()
			return callPayloadRetryDelay(failures), false, dropped, exportErr
		}
		// Give up on this batch alone; the rest of the queue gets a fresh
		// start. This can leave a client's closure permanently partial
		// (see CallPayloadMaxExportAttempts), so name the casualties.
		slog.Warn("dropping protected telemetry after repeated export failures",
			"kind", batcher.kind,
			"count", batchSize,
			"attempts", failures,
			"digests", batcher.describe(batch),
			"err", exportErr)
		dropErr := fmt.Errorf("dropping %d protected %s after %d failed exports: %w", batchSize, batcher.kind, failures, exportErr)
		batcher.mu.Lock()
		batcher.terminalErr = errors.Join(batcher.terminalErr, dropErr)
		batcher.mu.Unlock()
		dropped = errors.Join(dropped, dropErr)
		failures = 0
		clear(batch)
		queued = queued[batchSize:]
	}
	batcher.mu.Lock()
	batcher.failures = 0
	more = len(batcher.queue) > 0
	batcher.mu.Unlock()
	return 0, more, dropped, nil
}

func (batcher *protectedBatcher[T]) deliveryError() error {
	batcher.mu.Lock()
	defer batcher.mu.Unlock()
	return batcher.terminalErr
}

func callPayloadRetryDelay(failures int) time.Duration {
	delay := CallPayloadRetryBaseDelay
	for i := 1; i < failures && delay < CallPayloadRetryMaxDelay; i++ {
		delay *= 2
	}
	return min(delay, CallPayloadRetryMaxDelay)
}

// callPayloadDigests lists the recipe digests stamped on the records, for
// diagnostics; records without the attribute are reported as "?".
func callPayloadDigests(records []sdklog.Record) []string {
	digests := make([]string, 0, len(records))
	for _, record := range records {
		digest := "?"
		record.WalkAttributes(func(attr log.KeyValue) bool {
			if attr.Key == telemetryattrs.CallPayloadDigestAttr && attr.Value.Kind() == log.KindString {
				digest = attr.Value.AsString()
				return false
			}
			return true
		})
		digests = append(digests, digest)
	}
	return digests
}

// WithoutCallPayloads wraps an ordinary log processor so neither call payloads
// nor revisioned agent controls reach its bounded queue. Both have their own
// protected transport; ordinary processing would duplicate them and allow a
// recipe burst to evict exec output.
func WithoutCallPayloads(next sdklog.Processor) sdklog.Processor {
	return withoutCallPayloadsProcessor{next: next}
}

type withoutCallPayloadsProcessor struct {
	next sdklog.Processor
}

func (p withoutCallPayloadsProcessor) OnEmit(ctx context.Context, record *sdklog.Record) error {
	if record != nil && (IsCallPayloadRecord(*record) || agentcontrol.IsRecord(*record)) {
		return nil
	}
	return p.next.OnEmit(ctx, record)
}

func (p withoutCallPayloadsProcessor) Enabled(ctx context.Context, params sdklog.EnabledParameters) bool {
	if enabler, ok := p.next.(interface {
		Enabled(context.Context, sdklog.EnabledParameters) bool
	}); ok {
		return enabler.Enabled(ctx, params)
	}
	return true
}

func (p withoutCallPayloadsProcessor) Shutdown(ctx context.Context) error {
	return p.next.Shutdown(ctx)
}

func (p withoutCallPayloadsProcessor) ForceFlush(ctx context.Context) error {
	return p.next.ForceFlush(ctx)
}

// IsCallPayloadRecord reports whether a record is on the call payload channel:
// a bytes body whose content type attribute names an encoded call.
func IsCallPayloadRecord(record sdklog.Record) bool {
	if record.Body().Kind() != log.KindBytes {
		return false
	}
	payload := false
	record.WalkAttributes(func(attr log.KeyValue) bool {
		if attr.Key == otelgo.ContentTypeAttr {
			payload = attr.Value.Kind() == log.KindString &&
				attr.Value.AsString() == telemetryattrs.CallPayloadContentType
			return false
		}
		return true
	})
	return payload
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
