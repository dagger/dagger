package sdklog

import (
	"context"

	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	otrace "go.opentelemetry.io/otel/trace"
)

var _ log.Logger = &logger{}

type logger struct {
	embedded.Logger

	provider             *LoggerProvider
	resource             *resource.Resource
	instrumentationScope instrumentation.Scope
}

func (l logger) Emit(ctx context.Context, r log.Record) {
	span := otrace.SpanFromContext(ctx)

	log := &LogData{
		Record:   r,
		TraceID:  span.SpanContext().TraceID(),
		SpanID:   span.SpanContext().SpanID(),
		Resource: l.resource,
	}

	for _, proc := range l.provider.getLogProcessors() {
		proc.OnEmit(ctx, log)
	}
}
