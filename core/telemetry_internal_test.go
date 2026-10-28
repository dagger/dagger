package core

import (
	"context"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

func TestAroundFuncMarksTrivialFieldsInternal(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithSpanProcessor(recorder),
	)
	ctx, root := provider.Tracer("test").Start(context.Background(), "root")
	defer root.End()

	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = dagql.ContextWithTrivialField(ctx)

	req := &dagql.CallRequest{
		ResultCall:       testResultCall("value", dagql.String(""), nil),
		ReceiverTypeName: "Value",
	}
	_, done := AroundFunc(ctx, req)
	var callErr error
	done(nil, false, &callErr)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	require.True(t, otelprofAttrBool(spans[0], telemetry.UIInternalAttr))
}

func TestAroundFuncDeduplicatesUncachedScalarGetters(t *testing.T) {
	for _, tc := range []struct {
		field string
		spans int
	}{
		{field: "name", spans: 1},
		{field: "read", spans: 3},
	} {
		t.Run(tc.field, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := trace.NewTracerProvider(
				trace.WithSampler(trace.AlwaysSample()),
				trace.WithSpanProcessor(recorder),
			)
			ctx, root := provider.Tracer("test").Start(t.Context(), "root")
			defer func() {
				root.End()
				require.NoError(t, provider.Shutdown(context.Background()))
			}()
			ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
				ClientID: "getter-test", SessionID: "getter-test",
			})
			cache, err := dagql.NewCache(ctx, "", nil, nil)
			require.NoError(t, err)
			ctx = dagql.ContextWithCache(ctx, cache)
			defer func() {
				require.NoError(t, cache.ReleaseSession(ctx, "getter-test"))
				require.NoError(t, cache.Close(ctx))
			}()
			ctx = ContextWithQuery(ctx, &Query{Server: &payloadRoutingTestServer{
				mockServer:   &mockServer{},
				payloadStore: &testSeenKeys{},
				spanStore:    &testSpanSeenKeys{},
			}})
			srv, err := dagql.NewServer(ctx, &Query{})
			require.NoError(t, err)
			dagql.Fields[EnvVariable]{
				dagql.Func("read", func(context.Context, EnvVariable, struct{}) (dagql.String, error) {
					return "example", nil
				}).DoNotCache("Read current state"),
			}.Install(srv)
			dagql.Fields[*Query]{
				dagql.Func("envVariable", func(context.Context, *Query, struct{}) (EnvVariable, error) {
					return EnvVariable{Name: "example"}, nil
				}),
			}.Install(srv)
			ctx = dagql.ContextWithServer(ctx, srv)
			var item dagql.ObjectResult[EnvVariable]
			err = srv.Select(ctx, srv.Root(), &item, dagql.Selector{Field: "envVariable"})
			require.NoError(t, err)
			srv.Around(AroundFunc)
			read := func(ctx context.Context) {
				for range 3 {
					res, err := item.Select(ctx, srv, dagql.Selector{Field: tc.field})
					require.NoError(t, err)
					require.Equal(t, dagql.String("example"), res.Unwrap())
					require.False(t, res.HitCache())
				}
			}
			spanCount := func() int {
				count := 0
				for _, span := range recorder.Ended() {
					if span.Name() == "EnvVariable."+tc.field {
						count++
					}
				}
				return count
			}
			read(ctx)
			require.Equal(t, tc.spans, spanCount())
			read(dagql.WithRepeatedTelemetry(ctx))
			require.Equal(t, 2*tc.spans, spanCount())
		})
	}
}
