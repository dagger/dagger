package server

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

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
}

func newEngineEventEmitter(logger log.Logger) *engineEventEmitter {
	e := &engineEventEmitter{
		queue: make(chan queuedEngineEvent, engineEventQueueSize),
		done:  make(chan struct{}),
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
// event has been handed to the logger.
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
		return context.Cause(ctx)
	}
}

func (e *engineEventEmitter) run(logger log.Logger) {
	defer close(e.done)
	ctx := context.Background()
	for queued := range e.queue {
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
		rec.SetBody(log.StringValue(string(body)))
		rec.AddAttributes(
			log.String(telemetryattrs.EngineEventAttr, queued.kind),
			log.String(telemetryattrs.EngineCacheAttr, queued.cache),
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

// emitShareEvent reports the parts one snapshot-sharing pass completed. The
// cache calls it on its sharing worker.
func (srv *Server) emitShareEvent(parts []dagql.SnapshotSharedPart) {
	if srv.engineEvents == nil || srv.engineCache == nil || len(parts) == 0 {
		return
	}
	event := telemetryattrs.EngineShareEvent{Parts: make([]telemetryattrs.EngineSharedPart, len(parts))}
	for i, part := range parts {
		event.Parts[i] = telemetryattrs.EngineSharedPart{ResultID: part.ResultID, Part: part.Part, Deps: part.Deps, Replacements: part.Replacements}
	}
	srv.engineEvents.Emit(telemetryattrs.EngineEventShare, srv.engineCache.Identity(), time.Now(), event)
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
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), srv.engineEventShutdownBudget)
	defer cancel()
	if err := srv.engineEvents.Close(ctx); err != nil {
		slog.Warn("engine cache events not drained before shutdown", "error", err, "dropped", srv.engineEvents.Dropped())
	}
	if err := srv.engineEventExport.Shutdown(ctx); err != nil {
		slog.Warn("engine cache events not exported before shutdown", "error", err)
	}
}
