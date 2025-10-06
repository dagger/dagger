package client

import (
	"context"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/trace"
)

const InstrumentationLibrary = "dagger.io/engine.client"

func Tracer(ctx context.Context) trace.Tracer {
	return telemetry.Tracer(ctx, InstrumentationLibrary)
}
