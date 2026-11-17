package telemetry

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/engine/slog"
)

const (
	blockingLogRetryBaseDelay = 100 * time.Millisecond
	blockingLogRetryMaxDelay  = 5 * time.Second
	// blockingLogWarnInterval spaces the warnings of a batch that keeps
	// failing, for example against an organization Cloud refuses.
	blockingLogWarnInterval = time.Minute
	// droppingLogExportTimeout is the SDK BatchProcessor's default.
	droppingLogExportTimeout = 30 * time.Second
)

// BlockingLogProcessor batches log records to an exporter without ever
// dropping one. Unlike the SDK BatchProcessor, which overwrites its oldest
// record when its queue is full, OnEmit blocks while the queue is full, and a
// batch whose export fails is retried with backoff until it lands or the
// processor is shut down. It is for producers that must learn about loss
// themselves: they emit from a goroutine that can wait, behind their own
// bounded, counted queue.
type BlockingLogProcessor struct {
	exporter  sdklog.Exporter
	limit     int
	batchSize int
	interval  time.Duration
	// dropping selects DroppingLogProcessor's policy: a full queue drops
	// its oldest record, and a failed export is not retried. Each export
	// then gives up after exportTimeout.
	dropping      bool
	exportTimeout time.Duration

	mu      sync.Mutex
	queue   []sdklog.Record
	space   chan struct{} // closed and replaced whenever queue space frees
	stopped bool

	wake      chan struct{}
	flush     chan chan struct{}
	stop      chan struct{}
	done      chan struct{}
	runCtx    context.Context
	cancelRun context.CancelFunc

	shutdownOnce sync.Once
	shutdownDone chan struct{}
	shutdownErr  error // published by closing shutdownDone
}

// NewBlockingLogProcessor exports to exporter in batches of at most batchSize
// records, at most interval after the first record of a batch arrives, with
// at most queueSize records waiting.
func NewBlockingLogProcessor(exporter sdklog.Exporter, queueSize, batchSize int, interval time.Duration) *BlockingLogProcessor {
	return newLogProcessor(exporter, queueSize, batchSize, interval, false, 0)
}

func newLogProcessor(exporter sdklog.Exporter, queueSize, batchSize int, interval time.Duration, dropping bool, exportTimeout time.Duration) *BlockingLogProcessor {
	runCtx, cancel := context.WithCancel(context.Background())
	p := &BlockingLogProcessor{
		exporter:      exporter,
		limit:         max(queueSize, 1),
		batchSize:     max(batchSize, 1),
		interval:      interval,
		dropping:      dropping,
		exportTimeout: exportTimeout,
		space:         make(chan struct{}),
		wake:          make(chan struct{}, 1),
		flush:         make(chan chan struct{}),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		runCtx:        runCtx,
		cancelRun:     cancel,
		shutdownDone:  make(chan struct{}),
	}
	go p.run()
	return p
}

// DroppingLogProcessor is a drop-in for the SDK's BatchProcessor, with the
// same loss policy — when its queue is full it drops the oldest record, and a
// batch whose export fails is reported to otel.Handle and dropped — but
// without its idle cost. The SDK's poll loop wakes every interval and clones
// its whole batch-sized []Record buffer (432 bytes a record) on every wake,
// records or not, so a 512-record batch at 100ms allocates over 2 MiB/s for
// as long as the process lives. This one only wakes when a record arrives,
// and exports at most interval after the first record of a batch, or as soon
// as a batch is full.
type DroppingLogProcessor struct {
	*BlockingLogProcessor
}

// NewDroppingLogProcessor exports to exporter in batches of at most batchSize
// records, at most interval after the first record of a batch arrives, with
// at most queueSize records waiting.
func NewDroppingLogProcessor(exporter sdklog.Exporter, queueSize, batchSize int, interval time.Duration) *DroppingLogProcessor {
	return newDroppingLogProcessor(exporter, queueSize, batchSize, interval, droppingLogExportTimeout)
}

func newDroppingLogProcessor(exporter sdklog.Exporter, queueSize, batchSize int, interval, exportTimeout time.Duration) *DroppingLogProcessor {
	return &DroppingLogProcessor{newLogProcessor(exporter, queueSize, batchSize, interval, true, exportTimeout)}
}

// OnEmit queues a copy of the record, waiting while the queue is full (a
// DroppingLogProcessor drops its oldest record instead). It returns ctx's
// error if ctx ends first, and drops nothing otherwise; after Shutdown it
// discards the record.
func (p *BlockingLogProcessor) OnEmit(ctx context.Context, record *sdklog.Record) error {
	if record == nil {
		return nil
	}
	cloned := record.Clone()
	for {
		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			return nil
		}
		if p.dropping && len(p.queue) >= p.limit {
			// Overwrite the oldest record, like the SDK's ring buffer.
			p.queue = p.queue[len(p.queue)-p.limit+1:]
		}
		if len(p.queue) < p.limit {
			p.queue = append(p.queue, cloned)
			signal := len(p.queue) == 1 || len(p.queue) >= p.batchSize
			p.mu.Unlock()
			if signal {
				select {
				case p.wake <- struct{}{}:
				default:
				}
			}
			return nil
		}
		space := p.space
		p.mu.Unlock()
		select {
		case <-space:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

func (*BlockingLogProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool {
	return true
}

// ForceFlush waits until every record queued before the call is exported.
func (p *BlockingLogProcessor) ForceFlush(ctx context.Context) error {
	reply := make(chan struct{})
	select {
	case p.flush <- reply:
	case <-p.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	select {
	case <-reply:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	if p.dropping {
		// Like the SDK BatchProcessor, flush the exporter too.
		return p.exporter.ForceFlush(ctx)
	}
	return nil
}

// Shutdown stops accepting records and drains the queue within ctx. It returns
// when ctx ends even if the exporter ignores cancellation. Exporter shutdown
// runs once, after the export worker exits, so it cannot race an active Export.
// Records still queued when ctx ends may be lost; an uncooperative exporter
// can keep the background cleanup goroutine alive until process exit.
func (p *BlockingLogProcessor) Shutdown(ctx context.Context) error {
	p.shutdownOnce.Do(func() {
		p.mu.Lock()
		p.stopped = true
		close(p.stop)
		close(p.space) // release blocked producers
		p.space = make(chan struct{})
		p.mu.Unlock()
		go func() {
			select {
			case <-p.done:
			case <-ctx.Done():
				p.cancelRun()
				<-p.done
			}
			exportErr := context.Cause(p.runCtx)
			p.cancelRun()
			p.shutdownErr = errors.Join(context.Cause(ctx), exportErr, p.exporter.Shutdown(ctx))
			close(p.shutdownDone)
		}()
	})
	select {
	case <-p.shutdownDone:
		return p.shutdownErr
	case <-ctx.Done():
		p.cancelRun()
		return context.Cause(ctx)
	}
}

func (p *BlockingLogProcessor) run() {
	defer func() {
		p.mu.Lock()
		p.queue = nil
		p.mu.Unlock()
		close(p.done)
	}()
	for {
		select {
		case <-p.runCtx.Done():
			return
		case <-p.wake:
			p.coalesce()
			p.exportQueued()
		case reply := <-p.flush:
			p.exportQueued()
			close(reply)
		case <-p.stop:
			p.exportQueued()
			return
		}
	}
}

// coalesce waits up to the interval for a batch to fill.
func (p *BlockingLogProcessor) coalesce() {
	p.mu.Lock()
	full := len(p.queue) >= p.batchSize
	p.mu.Unlock()
	if full || p.interval <= 0 {
		return
	}
	timer := time.NewTimer(p.interval)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return
		case <-p.stop:
			return
		case <-p.wake:
			p.mu.Lock()
			full := len(p.queue) >= p.batchSize
			p.mu.Unlock()
			if full {
				return
			}
		}
	}
}

// exportQueued exports every queued record, in order, a batch at a time.
func (p *BlockingLogProcessor) exportQueued() {
	for {
		if p.runCtx.Err() != nil {
			return
		}
		p.mu.Lock()
		n := min(len(p.queue), p.batchSize)
		if n == 0 {
			p.mu.Unlock()
			return
		}
		batch := p.queue[:n:n]
		p.queue = p.queue[n:]
		close(p.space)
		p.space = make(chan struct{})
		p.mu.Unlock()
		if !p.exportWithRetry(batch) {
			return
		}
	}
}

func (p *BlockingLogProcessor) exportWithRetry(batch []sdklog.Record) bool {
	delay := blockingLogRetryBaseDelay
	var (
		failures int
		warned   time.Time
	)
	for {
		if p.runCtx.Err() != nil {
			return false
		}
		exportCtx, cancel := p.runCtx, context.CancelFunc(func() {})
		if p.dropping {
			// Like the SDK BatchProcessor, give up on a stuck export.
			exportCtx, cancel = context.WithTimeout(p.runCtx, p.exportTimeout)
		}
		err := p.exporter.Export(exportCtx, batch)
		cancel()
		if err == nil {
			return p.runCtx.Err() == nil
		}
		if p.runCtx.Err() != nil {
			return false
		}
		if p.dropping {
			otel.Handle(err)
			return true
		}
		failures++
		if time.Since(warned) >= blockingLogWarnInterval {
			warned = time.Now()
			slog.Warn("log export failed; retrying", "records", len(batch), "failures", failures, "delay", delay, "error", err)
		}
		select {
		case <-time.After(delay):
		case <-p.runCtx.Done():
			return false
		}
		delay = min(delay*2, blockingLogRetryMaxDelay)
	}
}
