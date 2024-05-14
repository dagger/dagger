package buildkit

import (
	"context"
	"strings"

	"github.com/dagger/dagger/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

// buildkitTelemetryContext returns a context with a wrapped span that has a
// TracerProvider that can process spans produced by buildkit. This works,
// because of how buildkit heavily relies on trace.SpanFromContext.
func buildkitTelemetryContext(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	sp := trace.SpanFromContext(ctx)
	sp = buildkitSpan{Span: sp, provider: &buildkitTraceProvider{tp: sp.TracerProvider()}}
	return trace.ContextWithSpan(ctx, sp)
}

type buildkitTraceProvider struct {
	embedded.TracerProvider
	tp trace.TracerProvider
}

func (tp *buildkitTraceProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return &buildkitTracer{
		tracer:   tp.tp.Tracer(name, options...),
		provider: tp,
	}
}

type buildkitTracer struct {
	embedded.Tracer
	tracer   trace.Tracer
	provider *buildkitTraceProvider
}

func (t *buildkitTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	internal := false

	if rest, ok := strings.CutPrefix(spanName, InternalPrefix); ok {
		spanName = rest
		internal = true
	} else if rest, ok := strings.CutPrefix(spanName, "load cache: "+InternalPrefix); ok {
		spanName = "load cache: " + rest
		internal = true
	}

	if internal {
		opts = append([]trace.SpanStartOption{}, opts...)
		opts = append(opts, trace.WithAttributes(attribute.Bool(telemetry.UIInternalAttr, true)))
	}

	ctx, span := t.tracer.Start(ctx, spanName, opts...)
	newSpan := buildkitSpan{Span: span, provider: t.provider}
	return trace.ContextWithSpan(ctx, newSpan), newSpan
}

type buildkitSpan struct {
	trace.Span
	provider *buildkitTraceProvider
}

func (s buildkitSpan) TracerProvider() trace.TracerProvider {
	return s.provider
}
