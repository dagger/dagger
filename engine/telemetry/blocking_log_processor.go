package telemetry

import (
	"context"
	"errors"
	"sync"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/engine/slog"
)

const (
	blockingLogRetryBaseDelay = 100 * time.Millisecond
	blockingLogRetryMaxDelay  = 5 * time.Second
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
}

// NewBlockingLogProcessor exports to exporter in batches of at most batchSize
// records, at most interval after the first record of a batch arrives, with
// at most queueSize records waiting.
func NewBlockingLogProcessor(exporter sdklog.Exporter, queueSize, batchSize int, interval time.Duration) *BlockingLogProcessor {
	runCtx, cancel := context.WithCancel(context.Background())
	p := &BlockingLogProcessor{
		exporter:  exporter,
		limit:     max(queueSize, 1),
		batchSize: max(batchSize, 1),
		interval:  interval,
		space:     make(chan struct{}),
		wake:      make(chan struct{}, 1),
		flush:     make(chan chan struct{}),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		runCtx:    runCtx,
		cancelRun: cancel,
	}
	go p.run()
	return p
}

// OnEmit queues a copy of the record, waiting while the queue is full. It
// returns ctx's error if ctx ends first, and drops nothing otherwise; after
// Shutdown it discards the record.
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
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Shutdown stops accepting records, exports the queued ones within ctx, and
// shuts the exporter down. Records still queued when ctx ends are lost.
func (p *BlockingLogProcessor) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if !p.stopped {
		p.stopped = true
		close(p.stop)
		close(p.space) // release blocked producers
		p.space = make(chan struct{})
	}
	p.mu.Unlock()
	var err error
	select {
	case <-p.done:
	case <-ctx.Done():
		p.cancelRun()
		<-p.done
		err = context.Cause(ctx)
	}
	p.cancelRun()
	return errors.Join(err, p.exporter.Shutdown(ctx))
}

func (p *BlockingLogProcessor) run() {
	defer close(p.done)
	for {
		select {
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
	for {
		err := p.exporter.Export(p.runCtx, batch)
		if err == nil {
			return true
		}
		if p.runCtx.Err() != nil {
			return false
		}
		slog.Warn("log export failed; retrying", "records", len(batch), "delay", delay, "error", err)
		select {
		case <-time.After(delay):
		case <-p.runCtx.Done():
			return false
		}
		delay = min(delay*2, blockingLogRetryMaxDelay)
	}
}
