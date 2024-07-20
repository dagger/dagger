package buildkit

import (
	"context"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine"
)

// buildkitTelemetryContext returns a context with a wrapped span that has a
// TracerProvider that can process spans produced by buildkit. This works,
// because of how buildkit heavily relies on trace.SpanFromContext.
func buildkitTelemetryContext(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	sp := trace.SpanFromContext(ctx)
	lp := telemetry.LoggerProvider(ctx)
	// Start will sometimes be called with a ctx that drops our client metadata. No idea
	// why. I guess it gets lost somewhere in the buildkit solver.
	//
	// To work around that, we'll capture the client metadata here and reinstall it for
	// future spans.
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		slog.Warn("failed to extract client metadata from context", "err", err)
		// carry on
	}
	sp = buildkitSpan{
		Span: sp,
		tp: &buildkitTraceProvider{
			tp:             sp.TracerProvider(),
			lp:             lp,
			clientMetadata: clientMetadata,
		},
	}
	return trace.ContextWithSpan(ctx, sp)
}

type buildkitTraceProvider struct {
	embedded.TracerProvider
	tp             trace.TracerProvider
	lp             *sdklog.LoggerProvider
	clientMetadata *engine.ClientMetadata
}

func (tp *buildkitTraceProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return &buildkitTracer{
		tracer:         tp.tp.Tracer(name, options...),
		provider:       tp,
		clientMetadata: tp.clientMetadata,
		lp:             tp.lp,
	}
}

type buildkitTracer struct {
	embedded.Tracer
	tracer         trace.Tracer
	provider       *buildkitTraceProvider
	clientMetadata *engine.ClientMetadata
	lp             *sdklog.LoggerProvider
}

const TelemetryComponent = "buildkit"

func (t *buildkitTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	opts = append([]trace.SpanStartOption{
		// Sprinkle an attribute on these spans so we can mess with them in the SpanProcessor.
		//
		// Ideally Buildkit would just set an appropriate scope name, but it doesn't, so we
		// have to make do.
		trace.WithAttributes(attribute.Bool("buildkit", true)),
	}, opts...)

	// Bring back the client metadata since it's part of span processing.
	if _, err := engine.ClientMetadataFromContext(ctx); err != nil {
		ctx = engine.ContextWithClientMetadata(ctx, t.clientMetadata)
	}

	ctx = telemetry.WithLoggerProvider(ctx, t.lp)
	ctx, span := t.tracer.Start(ctx, spanName, opts...)
	newSpan := buildkitSpan{Span: span, tp: t.provider}
	return trace.ContextWithSpan(ctx, newSpan), newSpan
}

type buildkitSpan struct {
	trace.Span
	tp *buildkitTraceProvider
}

func (s buildkitSpan) TracerProvider() trace.TracerProvider {
	return s.tp
}

// SpanProcessor modifies spans coming from the Buildkit component to integrate
// them with Dagger's telemetry stack.
//
// It must be used in combination with the buildkitTraceProvider.
type SpanProcessor struct{}

var _ sdktrace.SpanProcessor = SpanProcessor{}

func (sp SpanProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	var isBuildkit bool
	var vertex string
	for _, attr := range span.Attributes() {
		switch attr.Key {
		case "buildkit":
			isBuildkit = attr.Value.AsBool()
		case "vertex":
			vertex = attr.Value.AsString()
		}
	}
	if !isBuildkit {
		return
	}
	spanName := span.Name()

	attrs := []attribute.KeyValue{}

	// convert [internal] prefix into internal attribute
	if rest, ok := strings.CutPrefix(spanName, InternalPrefix); ok {
		span.SetName(rest)
		attrs = append(attrs, attribute.Bool(telemetry.UIInternalAttr, true))
	} else if rest, ok := strings.CutPrefix(spanName, "load cache: "+InternalPrefix); ok {
		span.SetName("load cache: " + rest)
		attrs = append(attrs, attribute.Bool(telemetry.UIInternalAttr, true))
	}

	// silence noisy registry lookups
	if spanName == "remotes.docker.resolver.HTTPRequest" {
		attrs = append(attrs, attribute.Bool(telemetry.UIEncapsulatedAttr, true))
	}
	if spanName == "HTTP GET" {
		// HACK: resolver.do is wrapped with a new span, resolver.authorize isn't :)
		// so we need this special case, to make sure to catch the auth requests
		attrs = append(attrs, attribute.Bool(telemetry.UIEncapsulatedAttr, true))
	}

	// remap vertex attr to standard effect ID attr
	if vertex != "" {
		attrs = append(attrs, attribute.String(telemetry.EffectIDAttr, vertex))
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

func (sp SpanProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (sp SpanProcessor) ForceFlush(context.Context) error { return nil }
func (sp SpanProcessor) Shutdown(context.Context) error   { return nil }
