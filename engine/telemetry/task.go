package telemetry

import (
	"context"

	dagtel "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/trace"
)

func Task(ctx context.Context, name string, fn func(context.Context) error) (rerr error) {
	ctx, span := trace.SpanFromContext(ctx).TracerProvider().
		Tracer("dagger.io/engine/telemetry").
		Start(ctx, name, dagtel.Internal())
	defer dagtel.EndWithCause(span, &rerr)
	return fn(ctx)
}

func TaskRet[T any](ctx context.Context, name string, fn func(context.Context) (T, error)) (ret T, rerr error) {
	ctx, span := trace.SpanFromContext(ctx).TracerProvider().
		Tracer("dagger.io/engine/telemetry").
		Start(ctx, name, dagtel.Internal())
	defer dagtel.EndWithCause(span, &rerr)
	return fn(ctx)
}
