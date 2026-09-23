package server

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/log"

	"github.com/dagger/dagger/dagql/cachefact"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
)

// cacheFactQueueSize bounds the facts waiting to be encoded and logged.
const cacheFactQueueSize = 65536

type queuedCacheFact struct {
	fact cachefact.Fact
	at   time.Time
}

// cacheFactEmitter is the engine's dagql.FactSink. The cache calls Emit with
// its graph lock held, so Emit never blocks: it queues the fact, or drops it
// and counts the drop when the queue is full. One goroutine drains the queue,
// encodes each fact and emits it as one OTel log record through the logger
// provider of the context it was started with: the engine's process
// telemetry, never a session's.
type cacheFactEmitter struct {
	queue   chan queuedCacheFact
	dropped atomic.Uint64

	// closeMu orders Emit against Close; a fact emitted after Close is
	// dropped.
	closeMu sync.RWMutex
	closed  bool
	done    chan struct{}
}

func newCacheFactEmitter(ctx context.Context) *cacheFactEmitter {
	e := &cacheFactEmitter{
		queue: make(chan queuedCacheFact, cacheFactQueueSize),
		done:  make(chan struct{}),
	}
	go e.run(context.WithoutCancel(ctx))
	return e
}

func (e *cacheFactEmitter) Emit(fact cachefact.Fact) {
	e.closeMu.RLock()
	defer e.closeMu.RUnlock()
	if e.closed {
		e.dropped.Add(1)
		return
	}
	select {
	case e.queue <- queuedCacheFact{fact: fact, at: time.Now()}:
	default:
		e.dropped.Add(1)
	}
}

// Dropped returns the number of facts dropped since the engine started.
func (e *cacheFactEmitter) Dropped() uint64 {
	return e.dropped.Load()
}

// Close stops accepting facts and waits, bounded by ctx, until every queued
// fact has been handed to the logger provider. The provider's own flush
// delivers them.
func (e *cacheFactEmitter) Close(ctx context.Context) error {
	e.closeMu.Lock()
	if !e.closed {
		e.closed = true
		close(e.queue)
	}
	e.closeMu.Unlock()
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (e *cacheFactEmitter) run(ctx context.Context) {
	defer close(e.done)
	logger := telemetry.Logger(ctx, cachefact.ScopeName)
	for queued := range e.queue {
		body, err := json.Marshal(queued.fact)
		if err != nil {
			e.dropped.Add(1)
			slog.Warn("failed to encode cache fact", "kind", queued.fact.Kind(), "seq", queued.fact.Seq, "error", err)
			continue
		}
		var rec log.Record
		rec.SetTimestamp(queued.at)
		rec.SetObservedTimestamp(queued.at)
		rec.SetSeverity(log.SeverityInfo)
		rec.SetBody(log.StringValue(string(body)))
		rec.AddAttributes(
			log.String(cachefact.AttrKind, string(queued.fact.Kind())),
			log.String(cachefact.AttrSeq, strconv.FormatUint(queued.fact.Seq, 10)),
			log.String(cachefact.AttrVersion, cachefact.Version),
		)
		logger.Emit(ctx, rec)
	}
}
