package server

import (
	"context"

	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type executionScopeSpanProcessor struct{}

func newExecutionScopeSpanProcessor() sdktrace.SpanProcessor {
	return executionScopeSpanProcessor{}
}

func (executionScopeSpanProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return
	}
	if attrs := enginetel.ExecutionScopeAttributes(md); len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

func (executionScopeSpanProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (executionScopeSpanProcessor) ForceFlush(context.Context) error { return nil }
func (executionScopeSpanProcessor) Shutdown(context.Context) error   { return nil }
