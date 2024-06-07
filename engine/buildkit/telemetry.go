package buildkit

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"

	"dagger.io/dagger/telemetry"
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
	opts = append([]trace.SpanStartOption{}, opts...)

	if rest, ok := strings.CutPrefix(spanName, InternalPrefix); ok {
		spanName = rest
		opts = append(opts, telemetry.Internal())
	} else if rest, ok := strings.CutPrefix(spanName, "load cache: "+InternalPrefix); ok {
		spanName = "load cache: " + rest
		opts = append(opts, telemetry.Internal())
	}

	if spanName == "remotes.docker.resolver.HTTPRequest" {
		opts = append(opts, telemetry.Encapsulated())
	}
	if spanName == "HTTP GET" {
		// HACK: resolver.do is wrapped with a new span, resolver.authorize isn't :)
		// so we need this special case, to make sure to catch the auth requests
		opts = append(opts, telemetry.Encapsulated())
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
