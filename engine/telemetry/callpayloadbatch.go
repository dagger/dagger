package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	otelgo "github.com/dagger/otel-go"

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
// This is the ONLY transport for payload records (WithoutCallPayloads keeps
// them out of the ordinary processor), so it owns their retries too: a batch
// whose export fails goes back to the head of the queue, in order, and is
// retried with exponential backoff until it lands or
// CallPayloadMaxExportAttempts is spent.
type CallPayloadBatchProcessor struct {
	exporter sdklog.Exporter

	mu       sync.Mutex
	queue    []sdklog.Record
	failures int // consecutive failed exports of the batch at the queue head
	stopped  bool

	wake     chan struct{}
	flush    chan callPayloadBatchRequest
	shutdown chan callPayloadBatchRequest
	done     chan struct{}
}

type callPayloadBatchRequest struct {
	ctx  context.Context
	done chan error
}

func NewCallPayloadBatchProcessor(exporter sdklog.Exporter) *CallPayloadBatchProcessor {
	processor := &CallPayloadBatchProcessor{
		exporter: exporter,
		queue:    make([]sdklog.Record, 0, LogExportMaxBatchSize),
		wake:     make(chan struct{}, 1),
		flush:    make(chan callPayloadBatchRequest),
		shutdown: make(chan callPayloadBatchRequest, 1),
		done:     make(chan struct{}),
	}
	go processor.run()
	return processor
}

func (processor *CallPayloadBatchProcessor) OnEmit(_ context.Context, record *sdklog.Record) error {
	if record == nil || !IsCallPayloadRecord(*record) {
		return nil
	}

	cloned := record.Clone()
	processor.mu.Lock()
	if processor.stopped {
		processor.mu.Unlock()
		return nil
	}
	wake := len(processor.queue) == 0
	processor.queue = append(processor.queue, cloned)
	processor.mu.Unlock()

	if wake {
		select {
		case processor.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

func (processor *CallPayloadBatchProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool {
	return true
}

func (processor *CallPayloadBatchProcessor) ForceFlush(ctx context.Context) error {
	processor.mu.Lock()
	stopped := processor.stopped
	processor.mu.Unlock()
	if stopped {
		return nil
	}

	request := callPayloadBatchRequest{ctx: ctx, done: make(chan error, 1)}
	select {
	case processor.flush <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-processor.done:
		return nil
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-processor.done:
		return nil
	}
}

func (processor *CallPayloadBatchProcessor) Shutdown(ctx context.Context) error {
	processor.mu.Lock()
	if processor.stopped {
		processor.mu.Unlock()
		select {
		case <-processor.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	processor.stopped = true
	processor.mu.Unlock()

	request := callPayloadBatchRequest{ctx: ctx, done: make(chan error, 1)}
	processor.shutdown <- request
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run is the single worker. Between exports it sleeps on the wake signal; a
// wake arms the coalescing delay, a failed export arms the retry backoff
// instead, and an explicit flush or shutdown exports immediately either way.
func (processor *CallPayloadBatchProcessor) run() {
	defer close(processor.done)
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
	export := func(ctx context.Context) error {
		disarm()
		retryIn, err := processor.exportQueued(ctx)
		if retryIn > 0 {
			arm(retryIn)
		}
		return err
	}
	for {
		select {
		case <-processor.wake:
			if timerC == nil {
				arm(CallPayloadExportDelay)
			}
			// A pending retry backoff keeps its schedule; the new records
			// queue up behind the failed batch and export with it.
		case <-timerC:
			if err := export(context.Background()); err != nil {
				otel.Handle(err)
			}
		case request := <-processor.flush:
			request.done <- export(request.ctx)
		case request := <-processor.shutdown:
			request.done <- export(request.ctx)
			return
		}
	}
}

// exportQueued drains the queue in bounded exporter batches, in order. On a
// failed export the unexported tail (failed batch first) goes back to the
// head of the queue and retryIn says how long to back off before trying
// again; 0 means the queue is empty or the batch was given up on. Records
// arriving during a successful drain are picked up by the same drain, so
// ForceFlush and Shutdown return only once they observe the queue empty.
func (processor *CallPayloadBatchProcessor) exportQueued(ctx context.Context) (retryIn time.Duration, err error) {
	for {
		processor.mu.Lock()
		queued := processor.queue
		processor.queue = nil
		failures := processor.failures
		processor.mu.Unlock()
		if len(queued) == 0 {
			return 0, err
		}

		for len(queued) > 0 {
			batchSize := min(len(queued), LogExportMaxBatchSize)
			batch := queued[:batchSize]
			exportErr := processor.exporter.Export(ctx, batch)
			if exportErr == nil {
				failures = 0
				clear(batch)
				queued = queued[batchSize:]
				continue
			}
			failures++
			if failures < CallPayloadMaxExportAttempts {
				processor.mu.Lock()
				processor.queue = append(queued, processor.queue...)
				processor.failures = failures
				processor.mu.Unlock()
				return callPayloadRetryDelay(failures), errors.Join(err, exportErr)
			}
			// Give up on this batch alone; the rest of the queue gets a fresh
			// start. This can leave a client's closure permanently partial
			// (see CallPayloadMaxExportAttempts), so name the casualties.
			digests := callPayloadDigests(batch)
			slog.Warn("dropping call payload records after repeated export failures",
				"records", batchSize,
				"attempts", failures,
				"digests", digests,
				"err", exportErr)
			err = errors.Join(err, fmt.Errorf("dropping %d call payload records after %d failed exports: %w", batchSize, failures, exportErr))
			failures = 0
			clear(batch)
			queued = queued[batchSize:]
		}
		processor.mu.Lock()
		processor.failures = 0
		processor.mu.Unlock()
	}
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

// WithoutCallPayloads wraps a log processor so call payload records never
// reach it. Payloads have their own lossless transport
// (CallPayloadBatchProcessor); letting them into the ordinary bounded queue as
// well would clone and export every payload twice and, worse, let a recipe
// burst evict exec output from that queue.
func WithoutCallPayloads(next sdklog.Processor) sdklog.Processor {
	return withoutCallPayloadsProcessor{next: next}
}

type withoutCallPayloadsProcessor struct {
	next sdklog.Processor
}

func (p withoutCallPayloadsProcessor) OnEmit(ctx context.Context, record *sdklog.Record) error {
	if record != nil && IsCallPayloadRecord(*record) {
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
