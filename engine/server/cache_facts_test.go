package server

import (
	"context"
	"sync"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/dagql/cachefact"
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
