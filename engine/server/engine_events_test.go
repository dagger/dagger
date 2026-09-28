package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

type engineEventTestExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *engineEventTestExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, rec := range records {
		e.records = append(e.records, rec.Clone())
	}
	return nil
}
func (*engineEventTestExporter) Shutdown(context.Context) error   { return nil }
func (*engineEventTestExporter) ForceFlush(context.Context) error { return nil }

// engineEventTestExport is an EngineEventExport over a test logger provider.
type engineEventTestExport struct {
	provider *sdklog.LoggerProvider
}

func (e *engineEventTestExport) Logger() log.Logger {
	return e.provider.Logger(telemetryattrs.EngineEventScope)
}
func (e *engineEventTestExport) Shutdown(ctx context.Context) error {
	return e.provider.Shutdown(ctx)
}

// engineEventTestServer is a server with an engine event export whose
// records the exporter keeps.
func engineEventTestServer(cache *dagql.Cache) (*Server, *engineEventTestExporter) {
	exporter := &engineEventTestExporter{}
	export := &engineEventTestExport{provider: sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))}
	return &Server{
		engineName:                "engine-a",
		engineCache:               cache,
		engineEvents:              newEngineEventEmitter(export.Logger()),
		engineEventExport:         export,
		engineEventShutdownBudget: engineEventShutdownTimeout,
	}, exporter
}

// engineTestEvent is one exported engine event.
type engineTestEvent struct {
	kind  string
	cache string
	at    time.Time
	body  []byte
}

func (e *engineEventTestExporter) events(t *testing.T) []engineTestEvent {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	var events []engineTestEvent
	for _, rec := range e.records {
		require.Equal(t, telemetryattrs.EngineEventScope, rec.InstrumentationScope().Name)
		require.Equal(t, log.KindString, rec.Body().Kind(), "the body is a JSON string")
		attrs := map[string]string{}
		rec.WalkAttributes(func(kv log.KeyValue) bool {
			attrs[kv.Key] = kv.Value.AsString()
			return true
		})
		require.Len(t, attrs, 2, "the kind and the cache: %v", attrs)
		events = append(events, engineTestEvent{
			kind:  attrs[telemetryattrs.EngineEventAttr],
			cache: attrs[telemetryattrs.EngineCacheAttr],
			at:    rec.Timestamp(),
			body:  []byte(rec.Body().AsString()),
		})
	}
	return events
}

func decodeEngineEvent[T any](t *testing.T, event engineTestEvent) T {
	t.Helper()
	var body T
	require.NoError(t, json.Unmarshal(event.body, &body))
	return body
}

func openEngineEventTestCache(t *testing.T, path string) *dagql.Cache {
	t.Helper()
	cache, err := dagql.NewCache(t.Context(), path, nil, nil)
	require.NoError(t, err)
	return cache
}

// Every event carries its kind and the cache's identity and generation, is
// timed when it happened, and has its body as JSON. A prune run that dropped
// no edge sends nothing.
func TestEngineEventsCarryKindCacheAndBody(t *testing.T) {
	t.Parallel()
	ctx := boundedContext(t)
	cache := openEngineEventTestCache(t, filepath.Join(t.TempDir(), "cache.db"))
	srv, exporter := engineEventTestServer(cache)
	identity := cache.Identity()

	srv.startEngineEvents()
	first, second := time.Unix(100, 1), time.Unix(100, 2)
	srv.emitPruneEvent([]dagql.CacheRetentionDrop{{ResultID: 7, DroppedAt: first}, {ResultID: 9, DroppedAt: second}})
	srv.emitPruneEvent(nil)
	srv.emitShareEvent([]dagql.SnapshotSharedPart{{ResultID: 12, Part: `{"part":"snapshot"}`, Deps: []uint64{3, 4}}})
	require.NoError(t, cache.Close(ctx))
	srv.stopEngineEvents(ctx, true)

	events := exporter.events(t)
	require.Len(t, events, 4)
	for _, event := range events {
		require.Equal(t, identity.String(), event.cache)
	}
	require.Equal(t, telemetryattrs.EngineEventStart, events[0].kind)
	require.Equal(t, telemetryattrs.EngineStartEvent{EngineVersion: engine.Version, EngineName: "engine-a"}, decodeEngineEvent[telemetryattrs.EngineStartEvent](t, events[0]))

	require.Equal(t, telemetryattrs.EngineEventPrune, events[1].kind)
	require.Equal(t, telemetryattrs.EnginePruneEvent{Drops: []telemetryattrs.EngineRetentionDrop{
		{ResultID: 7, DroppedAtUnixNano: first.UnixNano()},
		{ResultID: 9, DroppedAtUnixNano: second.UnixNano()},
	}}, decodeEngineEvent[telemetryattrs.EnginePruneEvent](t, events[1]))
	require.True(t, events[1].at.Equal(second), "timed at the run's last drop")

	require.Equal(t, telemetryattrs.EngineEventShare, events[2].kind)
	require.Equal(t, telemetryattrs.EngineShareEvent{Parts: []telemetryattrs.EngineSharedPart{
		{ResultID: 12, Part: `{"part":"snapshot"}`, Deps: []uint64{3, 4}},
	}}, decodeEngineEvent[telemetryattrs.EngineShareEvent](t, events[2]))

	require.Equal(t, telemetryattrs.EngineEventStop, events[3].kind)
	require.Equal(t, telemetryattrs.EngineStopEvent{Clean: true}, decodeEngineEvent[telemetryattrs.EngineStopEvent](t, events[3]))
	require.Zero(t, srv.engineEvents.Dropped())
}

// A clean restart keeps the cache's identity at its next generation, and the
// start reports what the restore installed, type definitions aside.
func TestEngineEventsAcrossRestart(t *testing.T) {
	t.Parallel()
	ctx := boundedContext(t)
	path := filepath.Join(t.TempDir(), "cache.db")

	cache := openEngineEventTestCache(t, path)
	srv, exporter := engineEventTestServer(cache)
	srv.startEngineEvents()
	sessionCtx := addGCTestPersistable(t, cache, "restart", "kept", dagql.NewInt(1))
	typeDef := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "typeDef", Type: dagql.NewResultCallType(dagql.NewInt(0).Type()), ProfileSkip: true}
	_, err := cache.GetOrInitCall(sessionCtx, "restart", gcTestTypeResolver{}, &dagql.CallRequest{ResultCall: typeDef, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewResultForCall(dagql.NewInt(2), typeDef)
	})
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(sessionCtx, "restart"))
	require.NoError(t, cache.Close(ctx))
	srv.stopEngineEvents(ctx, true)
	events := exporter.events(t)
	require.Len(t, events, 2)
	firstIdentity := events[0].cache
	require.Equal(t, telemetryattrs.EngineStopEvent{Clean: true, SavedEntries: 2}, decodeEngineEvent[telemetryattrs.EngineStopEvent](t, events[1]))

	cache = openEngineEventTestCache(t, path)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	srv, exporter = engineEventTestServer(cache)
	srv.startEngineEvents()
	require.NoError(t, srv.engineEvents.Close(ctx))
	events = exporter.events(t)
	require.Len(t, events, 1)
	identity := cache.Identity()
	require.Equal(t, uint64(2), identity.Generation)
	require.Equal(t, firstIdentity, identity.ID+"/1", "the same identity as the first process's")
	require.Equal(t, identity.String(), events[0].cache)
	require.Equal(t, telemetryattrs.EngineStartEvent{EngineVersion: engine.Version, EngineName: "engine-a", Restored: true, RestoredEntries: 1}, decodeEngineEvent[telemetryattrs.EngineStartEvent](t, events[0]))
}

// A start that wipes the previous cache names it: whether the cache's own
// open wiped an unclean database, or the server's local cache reset deleted
// the database file.
func TestEngineStartNamesWipedCache(t *testing.T) {
	t.Parallel()
	ctx := boundedContext(t)

	t.Run("unclean", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "cache.db")
		cache := openEngineEventTestCache(t, path)
		wiped := cache.Identity().ID
		require.NoError(t, cache.CloseDiscardingPersistence())

		cache = openEngineEventTestCache(t, path)
		t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
		require.Equal(t, dagql.CachePersistenceResetUncleanShutdown, cache.PersistenceResetReason())
		srv, exporter := engineEventTestServer(cache)
		srv.noteWipedCacheID(cache.WipedCacheID())
		srv.startEngineEvents()
		require.NoError(t, srv.engineEvents.Close(ctx))
		events := exporter.events(t)
		require.Len(t, events, 1)
		require.Equal(t, telemetryattrs.EngineStartEvent{EngineVersion: engine.Version, EngineName: "engine-a", WipedCache: wiped}, decodeEngineEvent[telemetryattrs.EngineStartEvent](t, events[0]))
		require.NotEqual(t, wiped+"/1", events[0].cache, "under a new identity")
	})

	t.Run("local cache reset", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		cache := openEngineEventTestCache(t, filepath.Join(root, "dagql-cache.db"))
		wiped := cache.Identity().ID
		require.NoError(t, cache.Close(context.Background()))

		srv := &Server{rootDir: root, workerRootDir: filepath.Join(root, "worker")}
		require.NoError(t, srv.removeLocalCacheStateOnDisk(ctx))
		require.Equal(t, wiped, srv.wipedCacheID)
		// A later wipe in the same start does not replace the first.
		srv.noteWipedCacheID("later")
		require.Equal(t, wiped, srv.wipedCacheID)
	})
}

// Each prune run the engine makes, explicit or automatic, of disk or of
// metadata, reports the edges it dropped; a run with none sends nothing.
func TestPruneEventsFromGC(t *testing.T) {
	t.Parallel()
	ctx := boundedContext(t)
	cache := newGCTestCache(t)
	srv, exporter := engineEventTestServer(cache)
	srv.rootDir = t.TempDir()
	var cold []uint64
	addCold := func(name string) {
		sessionCtx, res := addGCTestPersistableResult(t, cache, name, name, dagql.NewInt(1))
		number, ok := dagql.CacheResultNumber(res)
		require.True(t, ok)
		cold = append(cold, number)
		require.NoError(t, cache.ReleaseSession(sessionCtx, name))
	}
	before := time.Now()

	addCold("explicitDisk")
	_, err := srv.PruneEngineLocalCacheEntries(ctx, core.EngineCachePruneOptions{})
	require.NoError(t, err)
	_, err = srv.PruneEngineLocalCacheEntries(ctx, core.EngineCachePruneOptions{})
	require.NoError(t, err)

	addCold("explicitMetadata")
	maximum, target := cache.MetadataEstimate().EstimatedBytes-1, int64(1)
	_, err = srv.PruneEngineLocalCacheEntries(ctx, core.EngineCachePruneOptions{MaxEstimatedBytes: &maximum, TargetEstimatedBytes: &target})
	require.NoError(t, err)

	addCold("automaticDisk")
	srv.workerGCPolicies = []dagqlCachePrunePolicy{{All: true}}
	require.NoError(t, srv.gcLocked(ctx, localCacheGCScheduled))
	srv.workerGCPolicies = nil

	addCold("automaticMetadata")
	srv.localCacheGCEnabled = true
	srv.dagqlCacheMaxEstimatedBytes = cache.MetadataEstimate().EstimatedBytes - 1
	srv.dagqlCacheTargetEstimatedBytes = 1
	require.NoError(t, srv.gcLocked(ctx, localCacheGCScheduled))
	require.NoError(t, srv.engineEvents.Close(ctx))

	events := exporter.events(t)
	require.Len(t, events, len(cold), "one event per run that dropped an edge")
	for i, event := range events {
		require.Equal(t, telemetryattrs.EngineEventPrune, event.kind)
		prune := decodeEngineEvent[telemetryattrs.EnginePruneEvent](t, event)
		require.Len(t, prune.Drops, 1)
		require.Equal(t, cold[i], prune.Drops[0].ResultID, "run %d drops its own entry's edge", i)
		require.False(t, time.Unix(0, prune.Drops[0].DroppedAtUnixNano).Before(before))
	}
}

type stalledEventExporter struct {
	engineEventTestExporter
	release chan struct{}
	once    sync.Once
}

func (e *stalledEventExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.once.Do(func() {
		select {
		case <-e.release:
		case <-ctx.Done():
		}
	})
	return e.engineEventTestExporter.Export(ctx, records)
}

// A burst of events far larger than the export processor's queue, against a
// stalled exporter, arrives whole: the processor holds the drain back instead
// of overwriting records, so the emitter's counted queue is the only place an
// event could drop, and none does.
func TestEngineEventBurstThroughBlockingExport(t *testing.T) {
	t.Parallel()
	ctx := boundedContext(t)
	exporter := &stalledEventExporter{release: make(chan struct{})}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(enginetel.NewBlockingLogProcessor(exporter, 16, 8, time.Millisecond)))
	e := newEngineEventEmitter(provider.Logger(telemetryattrs.EngineEventScope))

	const burst = 3000
	for i := range burst {
		e.Emit(telemetryattrs.EngineEventPrune, dagql.CacheIdentity{ID: "cache", Generation: 1}, time.Unix(0, int64(i)), telemetryattrs.EnginePruneEvent{})
	}
	close(exporter.release)
	require.NoError(t, e.Close(ctx))
	require.NoError(t, provider.Shutdown(ctx))
	require.Zero(t, e.Dropped())

	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	require.Len(t, exporter.records, burst)
	for i, rec := range exporter.records {
		require.Equal(t, int64(i), rec.Timestamp().UnixNano(), "in order")
	}
}

type unavailableEventExporter struct{}

func (unavailableEventExporter) Export(context.Context, []sdklog.Record) error {
	return errors.New("cloud unavailable")
}
func (unavailableEventExporter) Shutdown(context.Context) error   { return nil }
func (unavailableEventExporter) ForceFlush(context.Context) error { return nil }

// With Cloud unavailable and every queue full, the events' shutdown, the
// emitter's drain and the export's flush together, ends within one budget.
// An event emitted after the shutdown is dropped rather than panicking.
func TestEngineEventsShutdownWithinBudget(t *testing.T) {
	t.Parallel()
	ctx := boundedContext(t)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(enginetel.NewBlockingLogProcessor(unavailableEventExporter{}, 2, 1, 0)))
	export := &engineEventTestExport{provider: provider}
	srv := &Server{engineEvents: newEngineEventEmitter(export.Logger()), engineEventExport: export, engineEventShutdownBudget: 300 * time.Millisecond}
	for range 1000 {
		srv.engineEvents.Emit(telemetryattrs.EngineEventPrune, dagql.CacheIdentity{}, time.Now(), telemetryattrs.EnginePruneEvent{})
	}

	start := time.Now()
	srv.stopEngineEvents(ctx, false)
	elapsed := time.Since(start)
	require.Less(t, elapsed, 5*time.Second, "shutdown took %s with a 300ms budget", elapsed)
	select {
	case <-srv.engineEvents.done:
	case <-ctx.Done():
		t.Fatal("the emitter's drain did not end after the export shut down")
	}
	dropped := srv.engineEvents.Dropped()
	srv.engineEvents.Emit(telemetryattrs.EngineEventStop, dagql.CacheIdentity{}, time.Now(), telemetryattrs.EngineStopEvent{})
	require.Equal(t, dropped+1, srv.engineEvents.Dropped(), "an event after the shutdown is dropped")
}
