package core

import (
	"context"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dagger/dagger/dagql"
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
