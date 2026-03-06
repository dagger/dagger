package server

import (
	"context"
	"testing"

	daggertelemetry "dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestExecutionScopeSpanProcessorAddsScopeAttrs(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(newExecutionScopeSpanProcessor()),
		sdktrace.WithSpanProcessor(recorder),
	)

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		SessionID:  "session-1",
		ClientID:   "client-1",
		ClientKind: enginetel.ClientKindRoot,
	})
	_, span := tp.Tracer("test").Start(ctx, "test")
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(spans))
	}

	got := map[string]string{}
	for _, attr := range spans[0].Attributes() {
		got[string(attr.Key)] = attr.Value.AsString()
	}

	if got[daggertelemetry.EngineSessionIDAttr] != "session-1" {
		t.Fatalf("expected session attr, got %#v", got)
	}
	if got[daggertelemetry.EngineClientIDAttr] != "client-1" {
		t.Fatalf("expected client attr, got %#v", got)
	}
	if got[daggertelemetry.EngineClientKindAttr] != enginetel.ClientKindRoot {
		t.Fatalf("expected client kind attr, got %#v", got)
	}
}
