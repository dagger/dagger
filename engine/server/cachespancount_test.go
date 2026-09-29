package server

import (
	"context"
	"path/filepath"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// A session's spans and logs name the engine instance, the session and the
// engine's cache with its generation.
func TestSessionResourcesNameSessionAndCache(t *testing.T) {
	t.Parallel()
	cache, err := dagql.NewCache(t.Context(), filepath.Join(t.TempDir(), "cache.db"), nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	identity := cache.Identity()
	require.NotEmpty(t, identity.ID)
	require.Equal(t, uint64(1), identity.Generation)

	srv := &Server{engineInstanceID: "instance-1", engineCache: cache}
	attrs := srv.sessionResourceAttrs("session-1")
	for _, cloudEngine := range []bool{false, true} {
		res, err := sessionTracerResource(attrs, cloudEngine)
		require.NoError(t, err)
		requireResourceAttr(t, res.Set(), string(semconv.ServiceInstanceIDKey), "instance-1")
		requireResourceAttr(t, res.Set(), telemetryattrs.EngineSessionAttr, "session-1")
		requireResourceAttr(t, res.Set(), telemetryattrs.EngineCacheAttr, identity.ID+"/1")
		_, isCloud := res.Set().Value(attribute.Key(telemetryattrs.CloudEngineAttr))
		require.Equal(t, cloudEngine, isCloud)
	}
	res, err := withSessionResource(telemetry.Resource, attrs)
	require.NoError(t, err)
	requireResourceAttr(t, res.Set(), telemetryattrs.EngineSessionAttr, "session-1")
	requireResourceAttr(t, res.Set(), telemetryattrs.EngineCacheAttr, identity.ID+"/1")

	// A cache without persistence has no identity to name.
	memory, err := dagql.NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, memory.CloseDiscardingPersistence()) })
	srv = &Server{engineInstanceID: "instance-1", engineCache: memory}
	res, err = sessionTracerResource(srv.sessionResourceAttrs("session-1"), false)
	require.NoError(t, err)
	_, named := res.Set().Value(attribute.Key(telemetryattrs.EngineCacheAttr))
	require.False(t, named)
}

func requireResourceAttr(t *testing.T, set *attribute.Set, key, want string) {
	t.Helper()
	value, ok := set.Value(attribute.Key(key))
	require.True(t, ok, "resource has %s", key)
	require.Equal(t, want, value.AsString())
}

// cacheSpanTestSession is a session whose main client ran a traced query, with
// its own tracer provider and cache span counter, recording what it exports.
func cacheSpanTestSession(t *testing.T, id string, traceID trace.TraceID, wcprof *wcprofSpanCounter) (*daggerSession, *tracetest.SpanRecorder) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	sess := &daggerSession{
		sessionID:          id,
		mainClientCallerID: "main-" + id,
		cacheSpans:         &cacheSpanCounter{},
		wcprofTraceID:      traceID,
		wcprofRootSpanID:   trace.SpanID{1},
	}
	sess.clientRecords = map[string]*clientRecord{
		sess.mainClientCallerID: {clientID: sess.mainClientCallerID, clientMetadata: &engine.ClientMetadata{SessionID: id, ClientID: sess.mainClientCallerID}},
	}
	sess.tracerProvider = sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(wcprof),
		sdktrace.WithSpanProcessor(sess.cacheSpans),
		sdktrace.WithSpanProcessor(telemetryOriginSpanProcessor{sessionID: id}),
		sdktrace.WithSpanProcessor(rec),
	)
	t.Cleanup(func() { require.NoError(t, sess.tracerProvider.Shutdown(context.Background())) })
	return sess, rec
}

// emitTestSpan ends one span of the session's main client in its trace,
// naming a cache entry when resultID is set.
func emitTestSpan(sess *daggerSession, resultID string) {
	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{SessionID: sess.sessionID, ClientID: sess.mainClientCallerID})
	emitTestSpanIn(ctx, sess, resultID)
}

// emitTestSpanWithoutOrigin ends one span of the session that names no client,
// so the origin processor stamps no origin on it.
func emitTestSpanWithoutOrigin(sess *daggerSession, resultID string) {
	emitTestSpanIn(context.Background(), sess, resultID)
}

func emitTestSpanIn(ctx context.Context, sess *daggerSession, resultID string) {
	ctx = trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: sess.wcprofTraceID, SpanID: sess.wcprofRootSpanID, TraceFlags: trace.FlagsSampled,
	}))
	_, span := sess.tracerProvider.Tracer("test").Start(ctx, "call")
	if resultID != "" {
		span.SetAttributes(attribute.String(telemetryattrs.CacheResultIDAttr, resultID))
	}
	span.End()
}

// carrierAttrs returns the attributes of the session-end carrier the session
// exported, or false when it exported none.
func carrierAttrs(rec *tracetest.SpanRecorder) (map[string]string, bool) {
	for _, span := range rec.Ended() {
		if span.Name() != wcprofSessionCompleteSpanName {
			continue
		}
		attrs := map[string]string{}
		for _, kv := range span.Attributes() {
			attrs[string(kv.Key)] = kv.Value.Emit()
		}
		return attrs, true
	}
	return nil, false
}

// Only spans that end naming a cache entry count, and the carrier declares
// the count and is not counted itself.
func TestSessionCompleteDeclaresCacheSpans(t *testing.T) {
	t.Parallel()
	srv := &Server{wcprofSpanCount: newWcprofSpanCounter()}
	sess, rec := cacheSpanTestSession(t, "s", trace.TraceID{1}, srv.wcprofSpanCount)
	emitTestSpan(sess, "7")
	emitTestSpan(sess, "")
	emitTestSpan(sess, "8")
	srv.stampSessionComplete(context.Background(), sess)

	attrs, ok := carrierAttrs(rec)
	require.True(t, ok)
	require.Equal(t, "2", attrs[telemetryattrs.CacheSessionSpansAttr])
	require.Equal(t, "3", attrs[telemetryattrs.WcprofSessionSpanCountAttr])
	require.Equal(t, "true", attrs[telemetryattrs.WcprofSessionCompleteAttr])
	require.Equal(t, int64(2), sess.cacheSpans.Count(), "the carrier is not a cache span")
}

// Two sessions share one trace. The first to end reaps the trace's wcprof
// count, so the second, which started no span afterwards, has a wcprof count
// of 0. It still sends its carrier, with its own cache span count and no
// wcprof count; the first session's wcprof count is unchanged.
func TestSessionCompleteWithSharedTrace(t *testing.T) {
	t.Parallel()
	srv := &Server{wcprofSpanCount: newWcprofSpanCounter()}
	traceID := trace.TraceID{2}
	a, recA := cacheSpanTestSession(t, "a", traceID, srv.wcprofSpanCount)
	b, recB := cacheSpanTestSession(t, "b", traceID, srv.wcprofSpanCount)
	emitTestSpan(a, "1")
	emitTestSpan(b, "2")
	emitTestSpan(b, "3")
	emitTestSpan(a, "")

	srv.stampSessionComplete(context.Background(), a)
	srv.wcprofSpanCount.Reap(traceID)
	srv.stampSessionComplete(context.Background(), b)
	srv.wcprofSpanCount.Reap(traceID)

	attrsA, ok := carrierAttrs(recA)
	require.True(t, ok)
	require.Equal(t, "1", attrsA[telemetryattrs.CacheSessionSpansAttr])
	require.Equal(t, "4", attrsA[telemetryattrs.WcprofSessionSpanCountAttr], "the trace's whole wcprof count, as before")

	attrsB, ok := carrierAttrs(recB)
	require.True(t, ok, "a session with cache spans sends its carrier")
	require.Equal(t, "2", attrsB[telemetryattrs.CacheSessionSpansAttr])
	_, hasWcprof := attrsB[telemetryattrs.WcprofSessionSpanCountAttr]
	require.False(t, hasWcprof, "no wcprof count of 0")
}

// A session with neither count sends no carrier; one that never recorded a
// traced main query sends none either.
func TestSessionCompleteNeedsACount(t *testing.T) {
	t.Parallel()
	srv := &Server{wcprofSpanCount: newWcprofSpanCounter()}
	empty, rec := cacheSpanTestSession(t, "empty", trace.TraceID{3}, srv.wcprofSpanCount)
	srv.stampSessionComplete(context.Background(), empty)
	_, ok := carrierAttrs(rec)
	require.False(t, ok)

	untraced, rec := cacheSpanTestSession(t, "untraced", trace.TraceID{4}, srv.wcprofSpanCount)
	emitTestSpan(untraced, "5")
	untraced.wcprofRootSpanID = trace.SpanID{}
	srv.stampSessionComplete(context.Background(), untraced)
	_, ok = carrierAttrs(rec)
	require.False(t, ok)
}

// A cache span without its origin client isn't counted: the session's span
// exporter drops it before the store, so the Cloud never reads it, and the
// carrier declares only the spans the Cloud can read.
func TestCacheSpanWithoutOriginIsNotCounted(t *testing.T) {
	t.Parallel()
	srv := &Server{wcprofSpanCount: newWcprofSpanCounter()}
	sess, rec := cacheSpanTestSession(t, "s", trace.TraceID{5}, srv.wcprofSpanCount)
	emitTestSpan(sess, "7")
	emitTestSpanWithoutOrigin(sess, "8")
	srv.stampSessionComplete(context.Background(), sess)

	attrs, ok := carrierAttrs(rec)
	require.True(t, ok)
	require.Equal(t, "1", attrs[telemetryattrs.CacheSessionSpansAttr])
	require.Equal(t, int64(1), sess.cacheSpans.Count())
}
