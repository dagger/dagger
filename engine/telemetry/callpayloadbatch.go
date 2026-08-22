package telemetry

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

const (
	CallPayloadQueueSize   = LogQueueSize
	CallPayloadExportDelay = 5 * time.Millisecond
)

// CallPayloadBatchProcessor gives immutable call payloads a short, on-demand
// path to the session exporter while the ordinary log processor retains its
// lower-frequency batching. OnEmit only clones and enqueues matching records;
// one worker wakes on the first payload, briefly coalesces its recipe closure,
// then drains it in bounded exporter batches. The ordinary processor later
// exports the same records, but the session exporter's per-target claims make
// those copies no-ops.
type CallPayloadBatchProcessor struct {
	exporter sdklog.Exporter

	mu      sync.Mutex
	queue   []sdklog.Record
	stopped bool

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
	if record == nil || !isCallPayloadRecord(*record) {
		return nil
	}

	cloned := record.Clone()
	processor.mu.Lock()
	if processor.stopped || len(processor.queue) >= CallPayloadQueueSize {
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

func (processor *CallPayloadBatchProcessor) run() {
	defer close(processor.done)
	for {
		select {
		case <-processor.wake:
			timer := time.NewTimer(CallPayloadExportDelay)
			select {
			case <-timer.C:
				if err := processor.exportQueued(context.Background()); err != nil {
					otel.Handle(err)
				}
			case request := <-processor.flush:
				stopTimer(timer)
				request.done <- processor.exportQueued(request.ctx)
			case request := <-processor.shutdown:
				stopTimer(timer)
				request.done <- processor.exportQueued(request.ctx)
				return
			}
		case request := <-processor.flush:
			request.done <- processor.exportQueued(request.ctx)
		case request := <-processor.shutdown:
			request.done <- processor.exportQueued(request.ctx)
			return
		}
	}
}

// exportQueued drains every record currently queued in back-to-back bounded
// exporter calls. Records arriving during an export are picked up by the same
// drain; ForceFlush and Shutdown likewise continue until they observe the queue
// empty.
func (processor *CallPayloadBatchProcessor) exportQueued(ctx context.Context) error {
	var errs error
	for {
		processor.mu.Lock()
		queued := processor.queue
		processor.queue = nil
		processor.mu.Unlock()
		if len(queued) == 0 {
			return errs
		}

		for len(queued) > 0 {
			batchSize := min(len(queued), LogExportMaxBatchSize)
			batch := queued[:batchSize]
			errs = errors.Join(errs, processor.exporter.Export(ctx, batch))
			clear(batch)
			queued = queued[batchSize:]
		}
	}
}

func isCallPayloadRecord(record sdklog.Record) bool {
	if record.InstrumentationScope().Name != telemetryattrs.CallPayloadInstrumentationScope ||
		record.Body().Kind() != log.KindBytes {
		return false
	}

	marker := false
	record.WalkAttributes(func(attr log.KeyValue) bool {
		if attr.Key != telemetryattrs.DagCallPayloadAttr {
			return true
		}
		if attr.Value.Kind() != log.KindBool || !attr.Value.AsBool() {
			marker = false
			return false
		}
		marker = true
		return true
	})
	return marker
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}
