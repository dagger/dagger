package server

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// engineEventQueueSize bounds the events waiting to be encoded and logged.
const engineEventQueueSize = 4096

// EngineEventExport is where the engine's cache events go: a logger that
// delivers every record it accepts, and a bounded shutdown that flushes them.
type EngineEventExport interface {
	Logger() log.Logger
	Shutdown(context.Context) error
}

// engineEventShutdownTimeout bounds the events' whole shutdown: engine.stop,
// the emitter's drain and the export's flush.
const engineEventShutdownTimeout = 30 * time.Second

// engineEventShutdownBudget is the events' shutdown budget: with the state
// discarded, it fits in the stop's reserve, which is shorter.
func engineEventShutdownBudget(stateDiscarded bool) time.Duration {
	if stateDiscarded {
		return DiscardedStateStopReserve
	}
	return engineEventShutdownTimeout
}

type queuedEngineEvent struct {
	kind  string
	cache string
	at    time.Time
	body  any
}

// engineEventEmitter reports the engine's cache events: what happens to its
// dagql cache outside any session (see telemetryattrs.EngineEventScope). Emit
// never blocks its caller: it queues the event, or drops it and counts the
// drop when the queue is full. One goroutine drains the queue, encodes each
// event and emits it as one log record through the export's logger. That
// logger waits rather than drop a record, so the count is the whole loss
// before shutdown.
type engineEventEmitter struct {
	queue   chan queuedEngineEvent
	dropped atomic.Uint64

	// closeMu orders Emit against Close; an event emitted after Close is
	// dropped.
	closeMu sync.RWMutex
	closed  bool
	done    chan struct{}

	// emitCtx is the drain's context for the logger: Close cancels it when
	// its own context ends, so an emit waiting on a full export queue gives
	// up. The SDK's LoggerProvider.Shutdown waits for every emit in flight
	// before shutting its processors down, so an emit that waits for the
	// processor's shutdown would hold the export's shutdown to its deadline
	// and the drain forever.
	emitCtx    context.Context
	cancelEmit context.CancelFunc
}

func newEngineEventEmitter(logger log.Logger) *engineEventEmitter {
	emitCtx, cancelEmit := context.WithCancel(context.Background())
	e := &engineEventEmitter{
		queue:      make(chan queuedEngineEvent, engineEventQueueSize),
		done:       make(chan struct{}),
		emitCtx:    emitCtx,
		cancelEmit: cancelEmit,
	}
	go e.run(logger)
	return e
}

// Emit queues one event of kind about the cache, with the engine's time of
// the event and its body, one of telemetryattrs' engine event types.
func (e *engineEventEmitter) Emit(kind string, cache dagql.CacheIdentity, at time.Time, body any) {
	e.closeMu.RLock()
	defer e.closeMu.RUnlock()
	if e.closed {
		e.dropped.Add(1)
		return
	}
	select {
	case e.queue <- queuedEngineEvent{kind: kind, cache: cache.String(), at: at, body: body}:
	default:
		e.dropped.Add(1)
	}
}

// Dropped returns the number of events dropped since the engine started.
func (e *engineEventEmitter) Dropped() uint64 {
	return e.dropped.Load()
}

// Close stops accepting events and waits, bounded by ctx, until every queued
// event has been handed to the logger. When ctx ends first, the events not
// handed over yet are dropped, and the drain ends without waiting on the
// logger.
func (e *engineEventEmitter) Close(ctx context.Context) error {
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
		e.cancelEmit()
		// The drain now only counts what is left as dropped; wait for it, so
		// Dropped includes those, but not on a logger that ignores the
		// cancellation.
		select {
		case <-e.done:
		case <-time.After(engineEventDrainGrace):
		}
		return context.Cause(ctx)
	}
}

// engineEventDrainGrace bounds how long Close waits for the drain to count
// the events left once its context ended.
const engineEventDrainGrace = time.Second

func (e *engineEventEmitter) run(logger log.Logger) {
	defer close(e.done)
	defer e.cancelEmit()
	ctx := e.emitCtx
	for queued := range e.queue {
		if ctx.Err() != nil {
			e.dropped.Add(1)
			continue
		}
		body, err := json.Marshal(queued.body)
		if err != nil {
			e.dropped.Add(1)
			slog.Warn("failed to encode engine cache event", "kind", queued.kind, "error", err)
			continue
		}
		var rec log.Record
		rec.SetTimestamp(queued.at)
		rec.SetObservedTimestamp(queued.at)
		rec.SetSeverity(log.SeverityInfo)
		rec.SetBody(attribute.StringValue(string(body)))
		rec.AddAttributes(
			attribute.String(telemetryattrs.EngineEventAttr, queued.kind),
			attribute.String(telemetryattrs.EngineCacheAttr, queued.cache),
		)
		logger.Emit(ctx, rec)
	}
}

// startEngineEvents reports engine.start once the cache is open and restored.
func (srv *Server) startEngineEvents() {
	if srv.engineEvents == nil || srv.engineCache == nil {
		return
	}
	srv.engineEvents.Emit(telemetryattrs.EngineEventStart, srv.engineCache.Identity(), time.Now(), telemetryattrs.EngineStartEvent{
		EngineVersion:   engine.Version,
		EngineName:      srv.engineName,
		Restored:        srv.engineCache.OpenedExisting(),
		RestoredEntries: srv.engineCache.BootRestoredResults(),
		WipedCache:      srv.wipedCacheID,
	})
}

// emitPruneEvent reports the retention edges one prune run dropped, each with
// the time of its own drop. A run that dropped none is not reported.
func (srv *Server) emitPruneEvent(drops []dagql.CacheRetentionDrop) {
	if srv.engineEvents == nil || srv.engineCache == nil || len(drops) == 0 {
		return
	}
	event := telemetryattrs.EnginePruneEvent{Drops: make([]telemetryattrs.EngineRetentionDrop, len(drops))}
	for i, drop := range drops {
		event.Drops[i] = telemetryattrs.EngineRetentionDrop{ResultID: drop.ResultID, DroppedAtUnixNano: drop.DroppedAt.UnixNano()}
	}
	srv.engineEvents.Emit(telemetryattrs.EngineEventPrune, srv.engineCache.Identity(), drops[len(drops)-1].DroppedAt, event)
}

// emitShareEvent reports the parts one snapshot-sharing pass completed, or
// the parts entries' spans left out. The cache calls it on its sharing
// worker or the worker that makes those reports.
func (srv *Server) emitShareEvent(parts []dagql.SnapshotSharedPart) {
	if srv.engineEvents == nil || srv.engineCache == nil || len(parts) == 0 {
		return
	}
	event := telemetryattrs.EngineShareEvent{Parts: make([]telemetryattrs.EngineSharedPart, len(parts))}
	for i, part := range parts {
		event.Parts[i] = telemetryattrs.EngineSharedPart{ResultID: part.ResultID, Part: part.Part, Deps: part.Deps, Replacements: part.Replacements, RawSize: part.RawSize}
	}
	srv.engineEvents.Emit(telemetryattrs.EngineEventShare, srv.engineCache.Identity(), time.Now(), event)
}

// emitReadEvent reports the parts of entries work read. The cache calls it
// with each batch of reads not reported before.
func (srv *Server) emitReadEvent(reads []dagql.PartRead) {
	if srv.engineEvents == nil || srv.engineCache == nil || len(reads) == 0 {
		return
	}
	event := telemetryattrs.EngineReadEvent{Reads: make([]telemetryattrs.EngineRead, len(reads))}
	for i, read := range reads {
		event.Reads[i] = telemetryattrs.EngineRead{ResultID: read.ResultID, Part: string(read.Part)}
	}
	srv.engineEvents.Emit(telemetryattrs.EngineEventRead, srv.engineCache.Identity(), time.Now(), event)
}

// stopEngineEvents runs after the cache closed: it reports engine.stop with
// what the close saved, drains the emitter and shuts the export down, all
// within one budget, so an unreachable Cloud delays shutdown by at most that
// much. Events still queued when it runs out are lost.
func (srv *Server) stopEngineEvents(ctx context.Context, clean bool) {
	if srv.engineEvents == nil {
		return
	}
	if srv.engineCache != nil {
		srv.engineEvents.Emit(telemetryattrs.EngineEventStop, srv.engineCache.Identity(), time.Now(), telemetryattrs.EngineStopEvent{
			Clean:        clean,
			SavedEntries: srv.engineCache.PersistedResults(),
		})
	}
	shutdownCtx := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok && srv.stateDiscarded {
		// The budget is part of the stop's reserve, which the final
		// telemetry flushes need too: it ends with the stop.
		var cancelAtDeadline context.CancelFunc
		shutdownCtx, cancelAtDeadline = context.WithDeadline(shutdownCtx, deadline)
		defer cancelAtDeadline()
	}
	ctx, cancel := context.WithTimeout(shutdownCtx, srv.engineEventShutdownBudget)
	defer cancel()
	if err := srv.engineEvents.Close(ctx); err != nil {
		slog.Warn("engine cache events not drained before shutdown", "error", err, "dropped", srv.engineEvents.Dropped())
	}
	if err := srv.engineEventExport.Shutdown(ctx); err != nil {
		slog.Warn("engine cache events not exported before shutdown", "error", err)
	}
}
