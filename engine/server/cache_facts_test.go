package server

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/cachefact"
	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

type cacheFactTestExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *cacheFactTestExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, rec := range records {
		e.records = append(e.records, rec.Clone())
	}
	return nil
}
func (*cacheFactTestExporter) Shutdown(context.Context) error   { return nil }
func (*cacheFactTestExporter) ForceFlush(context.Context) error { return nil }

func TestCacheFactEmitterLogsFactsOnProcessProvider(t *testing.T) {
	t.Parallel()
	exporter := &cacheFactTestExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	ctx := telemetry.WithLoggerProvider(boundedContext(t), provider)

	e := newCacheFactEmitter(ctx)
	facts := []cachefact.Fact{
		{Seq: 1, Body: cachefact.EngineStart{EngineVersion: "v", EngineName: "n", Boot: cachefact.BootFresh}},
		{Seq: 2, Body: cachefact.Deps{ID: 58, Deps: []uint64{12, 57}, Complete: true}},
	}
	for _, f := range facts {
		e.Emit(f)
	}
	require.NoError(t, e.Close(ctx))
	require.NoError(t, provider.ForceFlush(ctx))

	exporter.mu.Lock()
	records := exporter.records
	exporter.mu.Unlock()
	require.Len(t, records, len(facts))
	for i, rec := range records {
		require.Equal(t, cachefact.ScopeName, rec.InstrumentationScope().Name)
		attrs := map[string]string{}
		rec.WalkAttributes(func(kv log.KeyValue) bool {
			attrs[kv.Key] = kv.Value.AsString()
			return true
		})
		require.Equal(t, map[string]string{
			cachefact.AttrKind:    string(facts[i].Kind()),
			cachefact.AttrSeq:     []string{"1", "2"}[i],
			cachefact.AttrVersion: cachefact.Version,
		}, attrs)
		require.Equal(t, log.KindString, rec.Body().Kind(), "the body is a JSON string")
		decoded, err := cachefact.Decode([]byte(rec.Body().AsString()))
		require.NoError(t, err)
		require.Equal(t, facts[i], decoded)
		require.False(t, rec.Timestamp().IsZero())
	}
	require.Zero(t, e.Dropped())
}

// The cache emits with its graph lock held: a full queue drops and counts,
// and a fact emitted after Close is dropped rather than panicking.
func TestCacheFactEmitterNeverBlocks(t *testing.T) {
	t.Parallel()
	ctx := boundedContext(t)
	e := &cacheFactEmitter{queue: make(chan queuedCacheFact, 1), done: make(chan struct{})}
	fact := cachefact.Fact{Seq: 1, Body: cachefact.EngineAlive{}}
	e.Emit(fact)
	e.Emit(fact)
	require.EqualValues(t, 1, e.Dropped(), "a full queue drops")
	go e.run(ctx)
	require.NoError(t, e.Close(ctx))
	e.Emit(fact)
	require.EqualValues(t, 2, e.Dropped(), "a fact after Close is dropped")
}

func TestSessionResourcesNameEngineInstance(t *testing.T) {
	t.Parallel()
	for _, cloudEngine := range []bool{false, true} {
		res, err := sessionTracerResource("instance-1", cloudEngine)
		require.NoError(t, err)
		value, ok := res.Set().Value(attribute.Key(cachefact.ResourceEngineInstance))
		require.True(t, ok)
		require.Equal(t, "instance-1", value.AsString())
		_, isCloud := res.Set().Value(attribute.Key(telemetryattrs.CloudEngineAttr))
		require.Equal(t, cloudEngine, isCloud)
	}
	res, err := withEngineInstanceResource(telemetry.Resource, "instance-2")
	require.NoError(t, err)
	value, ok := res.Set().Value(attribute.Key(cachefact.ResourceEngineInstance))
	require.True(t, ok)
	require.Equal(t, "instance-2", value.AsString())
}

type cacheFactTestResolver struct{}

func (cacheFactTestResolver) ObjectType(string) (dagql.ObjectType, bool) { return nil, false }
func (cacheFactTestResolver) ScalarType(string) (dagql.ScalarType, bool) { return nil, false }

// The engine announces itself once its cache is open, reports liveness with
// its drop count, and announces its stop after the cache persisted, with
// every fact in one dense sequence on the process provider.
func TestCacheFactsEngineLifecycle(t *testing.T) {
	t.Parallel()
	exporter := &cacheFactTestExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)))
	ctx := telemetry.WithLoggerProvider(boundedContext(t), provider)

	srv := &Server{engineName: "engine-a", engineInstanceID: "instance-a", cacheFacts: newCacheFactEmitter(ctx)}
	cache, err := dagql.NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), nil, nil, dagql.WithFactSink(srv.cacheFacts), dagql.WithEngineInstanceID(srv.engineInstanceID))
	require.NoError(t, err)
	srv.engineCache = cache
	srv.startCacheFacts()

	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "retained", Type: dagql.NewResultCallType(dagql.Int(0).Type())}
	_, err = cache.GetOrInitCall(ctx, "session", cacheFactTestResolver{}, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewResultForCall(dagql.NewInt(1), frame)
	})
	require.NoError(t, err)
	srv.emitEngineAlive()
	require.NoError(t, cache.ReleaseSession(ctx, "session"))
	require.NoError(t, cache.Close(ctx))
	srv.stopCacheFacts(ctx, true)
	require.NoError(t, provider.ForceFlush(ctx))

	exporter.mu.Lock()
	records := exporter.records
	exporter.mu.Unlock()
	var facts []cachefact.Fact
	for _, rec := range records {
		fact, err := cachefact.Decode([]byte(rec.Body().AsString()))
		require.NoError(t, err)
		require.Equal(t, uint64(len(facts)+1), fact.Seq, "one dense sequence")
		facts = append(facts, fact)
	}
	require.GreaterOrEqual(t, len(facts), 4)
	require.Equal(t, cachefact.EngineStart{EngineVersion: engine.Version, EngineName: "engine-a", Boot: cachefact.BootFresh}, facts[0].Body)
	require.Contains(t, facts, cachefact.Fact{Seq: facts[len(facts)-2].Seq, Body: cachefact.EngineAlive{}}, "liveness reports no drops")
	require.Equal(t, cachefact.EngineStop{PersistedResults: 1, Clean: true}, facts[len(facts)-1].Body)
}

type stalledFactExporter struct {
	cacheFactTestExporter
	release chan struct{}
	once    sync.Once
}

func (e *stalledFactExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.once.Do(func() {
		select {
		case <-e.release:
		case <-ctx.Done():
		}
	})
	return e.cacheFactTestExporter.Export(ctx, records)
}

// A burst of facts far larger than the export processor's queue, against a
// stalled exporter, arrives whole: the processor holds the drain back instead
// of overwriting records, so the emitter's counted queue is the only place a
// fact could drop, and none does.
func TestCacheFactBurstThroughBlockingExport(t *testing.T) {
	t.Parallel()
	ctx := boundedContext(t)
	exporter := &stalledFactExporter{release: make(chan struct{})}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(enginetel.OnlyScope(cachefact.ScopeName, enginetel.NewBlockingLogProcessor(exporter, 16, 8, time.Millisecond))))
	e := newCacheFactEmitter(telemetry.WithLoggerProvider(ctx, provider))

	const burst = 5000
	for seq := uint64(1); seq <= burst; seq++ {
		e.Emit(cachefact.Fact{Seq: seq, Body: cachefact.EngineAlive{}})
	}
	close(exporter.release)
	require.NoError(t, e.Close(ctx))
	require.NoError(t, provider.Shutdown(ctx))
	require.Zero(t, e.Dropped())

	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	require.Len(t, exporter.records, burst)
	for i, rec := range exporter.records {
		fact, err := cachefact.Decode([]byte(rec.Body().AsString()))
		require.NoError(t, err)
		require.Equal(t, uint64(i+1), fact.Seq)
	}
}
