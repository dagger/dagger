package buildkit

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

const InstrumentationLibrary = "dagger.io/engine.buildkit"

func Tracer(ctx context.Context) trace.Tracer {
	return trace.SpanFromContext(ctx).TracerProvider().Tracer(InstrumentationLibrary)
}
