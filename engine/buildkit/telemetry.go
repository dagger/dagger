package buildkit

import (
	"context"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
	"go.opentelemetry.io/otel/trace/noop"

	"dagger.io/dagger/telemetry"
)

func WithTracePropagation(ctx context.Context) llb.ConstraintsOpt {
	mc := propagation.MapCarrier{}
	telemetry.Propagator.Inject(ctx, mc)
	return llb.WithDescription(mc)
}

func ContextFromDescription(desc map[string]string) context.Context {
	return telemetry.Propagator.Extract(context.Background(), propagation.MapCarrier(desc))
}

func SpanContextFromDescription(desc map[string]string) trace.SpanContext {
	return trace.SpanContextFromContext(ContextFromDescription(desc))
}

// buildkitTelemetryContext returns a context with a wrapped span that has a
// TracerProvider that can process spans produced by buildkit. This works,
// because of how buildkit heavily relies on trace.SpanFromContext.
func buildkitTelemetryContext(client *Client, ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	sp := trace.SpanFromContext(ctx)
	return trace.ContextWithSpan(ctx, buildkitSpan{
		Span: sp,
		tp: &buildkitTraceProvider{
			tp:     sp.TracerProvider(),
			lp:     telemetry.LoggerProvider(ctx),
			client: client,
		},
	})
}

type buildkitTraceProvider struct {
	embedded.TracerProvider
	tp     trace.TracerProvider
	lp     *sdklog.LoggerProvider
	client *Client
}

func (tp *buildkitTraceProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return &buildkitTracer{
		provider: tp,
		tracer:   tp.tp.Tracer(name, options...),
	}
}

type buildkitTracer struct {
	embedded.Tracer
	provider *buildkitTraceProvider
	tracer   trace.Tracer
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

	// Restore logger provider from the original ctx the provider was created.
	ctx = telemetry.WithLoggerProvider(ctx, t.provider.lp)

	if strings.HasPrefix(spanName, "cache request: ") {
		// these wrap calls to CacheMap (set deep inside buildkit)
		// we can discard these, they're not super useful to show to users
		return noop.NewTracerProvider().Tracer("").Start(ctx, spanName, opts...)
	}

	// Start the span, and make sure we return a span that has the provider.
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
type SpanProcessor struct {
	Client *Client
}

var _ sdktrace.SpanProcessor = SpanProcessor{}

func (sp SpanProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
	var isBuildkit bool
	var vertex digest.Digest
	for _, attr := range span.Attributes() {
		switch attr.Key {
		case "buildkit":
			isBuildkit = attr.Value.AsBool()
		case "vertex":
			vertex = digest.Digest(attr.Value.AsString())
		}
	}
	if !isBuildkit {
		return
	}

	// remap vertex attr to standard effect ID attr
	if vertex != "" {
		sp.setupVertex(span, vertex)
	}

	// convert [internal] prefix into internal attribute
	if rest, ok := strings.CutPrefix(span.Name(), InternalPrefix); ok {
		span.SetName(rest)
		span.SetAttributes(attribute.Bool(telemetry.UIInternalAttr, true))
	}

	// silence noisy registry lookups
	if span.Name() == "remotes.docker.resolver.HTTPRequest" {
		span.SetAttributes(attribute.Bool(telemetry.UIEncapsulatedAttr, true))
	}
	if span.Name() == "HTTP GET" {
		// HACK: resolver.do is wrapped with a new span, resolver.authorize isn't :)
		// so we need this special case, to make sure to catch the auth requests
		span.SetAttributes(attribute.Bool(telemetry.UIEncapsulatedAttr, true))
	}
}

func (sp SpanProcessor) setupVertex(span sdktrace.ReadWriteSpan, vertex digest.Digest) {
	span.SetAttributes(
		// track the "DAG digest" in the same way that we track Dagger digests
		attribute.String(telemetry.DagDigestAttr, vertex.String()),
		// track the "effect", which is tied to EffectIDsAttr on effect install (cause) site
		// TODO: this may be wholly redundant with above; it predates the use of span links
		attribute.String(telemetry.EffectIDAttr, vertex.String()),
	)

	llbOp, ok := sp.Client.LookupOp(vertex)
	if !ok {
		return
	}

	// link the vertex span to its causal span
	causeCtx := SpanContextFromDescription(llbOp.Metadata.Description)
	if causeCtx.IsValid() {
		span.AddLink(trace.Link{
			SpanContext: causeCtx,
			Attributes: []attribute.KeyValue{
				attribute.String(telemetry.EffectIDAttr, vertex.String()),
			},
		})
	}

	// track the inputs of the op
	// NOTE: this points to DagDigestAttr
	if len(llbOp.Inputs) > 0 {
		inputs := make([]string, len(llbOp.Inputs))
		for i, input := range llbOp.Inputs {
			inputs[i] = input.OpDigest.String()
		}
		span.SetAttributes(attribute.StringSlice(telemetry.DagInputsAttr, inputs))
	}

	// convert cache prefixes into cached attribute (this is set deep inside
	// buildkit)
	spanName, cached := strings.CutPrefix(span.Name(), "load cache: ")
	if cached {
		span.SetName(spanName)
		span.SetAttributes(attribute.Bool(telemetry.CachedAttr, true))

		// emit the op's deep set of inputs so that a cached op also implies
		// its inputs are cached, without requiring a span to be emitted for
		// each
		var cachedDigests []string
		llbOp.Walk(func(op *OpDAG) error {
			cachedDigests = append(cachedDigests, llbOp.OpDigest.String())
			return nil
		})
		span.SetAttributes(
			attribute.StringSlice(telemetry.CachedDigestsAttr, cachedDigests),
		)
	}
}

func (sp SpanProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (sp SpanProcessor) ForceFlush(context.Context) error { return nil }
func (sp SpanProcessor) Shutdown(context.Context) error   { return nil }
