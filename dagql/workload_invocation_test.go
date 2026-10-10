package dagql

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func invocationTestCache(t *testing.T) (context.Context, *Cache, *Server, *ResultCall, *tracetest.SpanRecorder) {
	t.Helper()
	ctx, cache := newPartsTestCache(t, nil)
	recorded := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorded))
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
	ctx = WithSkip(enginetel.WithWorkloadTracer(ctx, provider.Tracer(enginetel.WorkloadScope)))
	frame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType((&cacheTestPartsObject{}).Type()), Field: "hidden"}
	return ctx, cache, cacheTestPartsServer(t), frame, recorded
}

func TestWorkloadInvocationIncludesHiddenCalls(t *testing.T) {
	ctx, cache, server, frame, recorded := invocationTestCache(t)
	var runs atomic.Int32
	invoke := func() AnyResult {
		t.Helper()
		res, err := cache.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), server, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
			runs.Add(1)
			return NewObjectResultForCall(&cacheTestPartsObject{Value: 42}, server, frame)
		})
		require.NoError(t, err)
		return res
	}
	first := invoke()
	second := invoke()
	require.Equal(t, int32(1), runs.Load())
	require.Equal(t, first.cacheSharedResult().id, second.cacheSharedResult().id)
	spans := recorded.Ended()
	require.Len(t, spans, 2)
	outcome, _ := attrString(spans[0], telemetryattrs.CacheOutcomeAttr)
	require.Equal(t, "executed", outcome)
	outcome, _ = attrString(spans[1], telemetryattrs.CacheOutcomeAttr)
	require.Equal(t, "hit", outcome)
	firstDigest, _ := attrString(spans[0], telemetryattrs.DagContentPreferredDigestAttr)
	secondDigest, _ := attrString(spans[1], telemetryattrs.DagContentPreferredDigestAttr)
	require.NotEmpty(t, firstDigest)
	require.Equal(t, firstDigest, secondDigest)
}

func TestWorkloadInvocationOutlivesCanceledWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cache, server, frame, recorded := invocationTestCache(t)
		started, finish := make(chan struct{}), make(chan struct{})
		var runs atomic.Int32
		fn := func(context.Context) (AnyResult, error) {
			runs.Add(1)
			close(started)
			<-finish
			return NewObjectResultForCall(&cacheTestPartsObject{Value: 42}, server, frame)
		}
		invoke := func(ctx context.Context) error {
			_, err := cache.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), server,
				&CallRequest{ResultCall: frame, ConcurrencyKey: "shared"}, fn)
			return err
		}
		leaderCtx, cancel := context.WithCancel(ctx)
		leader, follower := make(chan error, 1), make(chan error, 1)
		go func() { leader <- invoke(leaderCtx) }()
		<-started
		go func() { follower <- invoke(ctx) }()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-leader, context.Canceled)
		require.Empty(t, recorded.Ended(), "a canceled waiter cannot finish the running operation")
		close(finish)
		require.NoError(t, <-follower)
		synctest.Wait()
		require.Equal(t, int32(1), runs.Load())
		spans := recorded.Ended()
		require.Len(t, spans, 2)
		outcomes := map[string]sdktrace.ReadOnlySpan{}
		for _, span := range spans {
			outcome, _ := attrString(span, telemetryattrs.CacheOutcomeAttr)
			outcomes[outcome] = span
		}
		require.Contains(t, outcomes, "executed")
		require.Contains(t, outcomes, "joined")
		require.NotEqual(t, codes.Error, outcomes["executed"].Status().Code)
	})
}
