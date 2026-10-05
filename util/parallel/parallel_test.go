package parallel

import (
	"context"
	"errors"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestJobPreservesErrorOrigin(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, origin := provider.Tracer("test").Start(context.Background(), "failing exec")
	cause := errors.New("exit code: 3")
	failure := telemetry.TrackOrigin(cause, origin.SpanContext())
	origin.End()

	err := New().WithContextualTracer(true).WithJob("generator", func(context.Context) error {
		return failure
	}).Run(ctx)
	require.ErrorIs(t, err, cause)
	spans := recorder.Ended()
	require.Len(t, spans, 2)
	require.Equal(t, codes.Error, spans[1].Status().Code)
	require.Equal(t, "exit code: 3", spans[1].Status().Description)
	require.Len(t, spans[1].Links(), 1)
	require.Equal(t, origin.SpanContext().TraceID(), spans[1].Links()[0].SpanContext.TraceID())
	require.Equal(t, origin.SpanContext().SpanID(), spans[1].Links()[0].SpanContext.SpanID())
}
