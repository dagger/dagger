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
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
)

// cacheFactQueueSize bounds the facts waiting to be encoded and logged.
const cacheFactQueueSize = 65536

type queuedCacheFact struct {
	fact cachefact.Fact
	at   time.Time
}

// CacheFactExport is where the engine's cache facts go: a logger that
// delivers every record it accepts, and a bounded shutdown that flushes them.
type CacheFactExport interface {
	Logger() log.Logger
	Shutdown(context.Context) error
}

// cacheFactShutdownTimeout bounds the facts' whole shutdown: engine.stop, the
// emitter's drain and the export's flush.
const cacheFactShutdownTimeout = 30 * time.Second

// cacheFactEmitter is the engine's dagql.FactSink. The cache calls Emit with
// its graph lock held, so Emit never blocks: it queues the fact, or drops it
// and counts the drop when the queue is full. One goroutine drains the queue,
// encodes each fact and emits it as one OTel log record through the fact
// logger. That logger waits rather than drop a record, so the count is the
// whole loss before shutdown.
type cacheFactEmitter struct {
	queue   chan queuedCacheFact
	dropped atomic.Uint64

	// closeMu orders Emit against Close; a fact emitted after Close is
	// dropped.
	closeMu sync.RWMutex
	closed  bool
	done    chan struct{}
}

func newCacheFactEmitter(logger log.Logger) *cacheFactEmitter {
	e := &cacheFactEmitter{
		queue: make(chan queuedCacheFact, cacheFactQueueSize),
		done:  make(chan struct{}),
	}
	go e.run(logger)
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
// fact has been handed to the fact logger.
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

func (e *cacheFactEmitter) run(logger log.Logger) {
	defer close(e.done)
	ctx := context.Background()
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

// cacheFactAliveInterval is how often a running engine emits engine.alive.
const cacheFactAliveInterval = 60 * time.Second

// startCacheFacts announces the engine once its cache is open, then reports
// liveness until stopCacheFacts.
func (srv *Server) startCacheFacts() {
	if srv.cacheFacts == nil || srv.engineCache == nil {
		return
	}
	restored := srv.engineCache.BootRestoredResults()
	boot := cachefact.BootFresh
	if restored > 0 {
		boot = cachefact.BootRestored
	}
	srv.engineCache.EmitFact(cachefact.EngineStart{
		EngineVersion:   engine.Version,
		EngineName:      srv.engineName,
		Boot:            boot,
		RestoredResults: restored,
	})

	stop := make(chan struct{})
	stopped := make(chan struct{})
	srv.cacheFactAliveStop = stop
	srv.cacheFactAliveStopped = stopped
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(cacheFactAliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				srv.emitEngineAlive()
			}
		}
	}()
}

func (srv *Server) emitEngineAlive() {
	srv.engineCache.EmitFact(cachefact.EngineAlive{DroppedFacts: srv.cacheFacts.Dropped()})
}

// stopCacheFacts runs after the cache closed: it stops liveness, announces the
// stop with what the close persisted, drains the emitter and shuts the export
// down, all within one budget, so an unreachable Cloud delays shutdown by at
// most that much. Facts still queued when it runs out are lost.
func (srv *Server) stopCacheFacts(ctx context.Context, clean bool) {
	if srv.cacheFacts == nil {
		return
	}
	if srv.cacheFactAliveStop != nil {
		close(srv.cacheFactAliveStop)
		<-srv.cacheFactAliveStopped
		srv.cacheFactAliveStop = nil
	}
	if srv.engineCache != nil {
		srv.engineCache.EmitFact(cachefact.EngineStop{PersistedResults: srv.engineCache.PersistedResults(), Clean: clean})
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), srv.cacheFactShutdownBudget)
	defer cancel()
	if err := srv.cacheFacts.Close(ctx); err != nil {
		slog.Warn("cache facts not drained before shutdown", "error", err, "dropped", srv.cacheFacts.Dropped())
	}
	if err := srv.cacheFactExport.Shutdown(ctx); err != nil {
		slog.Warn("cache facts not exported before shutdown", "error", err)
	}
}
