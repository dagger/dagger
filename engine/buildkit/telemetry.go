package buildkit

import (
	"context"
	"log/slog"
	"strings"
	"sync"

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

func WithPassthrough() llb.ConstraintsOpt {
	return llb.WithDescription(map[string]string{
		telemetry.UIPassthroughAttr: "true",
	})
}

func ContextFromDescription(ctx context.Context, desc map[string]string) context.Context {
	return telemetry.Propagator.Extract(ctx, propagation.MapCarrier(desc))
}

func SpanContextFromDescription(desc map[string]string) trace.SpanContext {
	return trace.SpanContextFromContext(ContextFromDescription(context.Background(), desc))
}

// buildkitTelemetryContext returns a context with a wrapped span that has a
// TracerProvider that can process spans produced by buildkit. This works,
// because of how buildkit heavily relies on trace.SpanFromContext.
func buildkitTelemetryProvider(client *Client, ctx context.Context) context.Context {
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

	witnessedOps  map[digest.Digest]bool
	witnessedOpsL sync.Mutex
}

func NewSpanProcessor(client *Client) *SpanProcessor {
	return &SpanProcessor{
		Client: client,

		witnessedOps: map[digest.Digest]bool{},
	}
}

var _ sdktrace.SpanProcessor = (*SpanProcessor)(nil)

func (sp *SpanProcessor) OnStart(ctx context.Context, span sdktrace.ReadWriteSpan) {
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

func (sp *SpanProcessor) setupVertex(span sdktrace.ReadWriteSpan, vertex digest.Digest) {
	llbOp, causeCtx, ok := sp.Client.LookupOp(vertex)
	if !ok {
		slog.Warn("op not found for vertex", "vertex", vertex)
		return
	}

	opCauseCtx := SpanContextFromDescription(llbOp.Metadata.Description)
	if opCauseCtx.IsValid() {
		causeCtx = opCauseCtx
	}

	if llbOp.Metadata.Description[telemetry.UIPassthroughAttr] != "" {
		span.SetAttributes(attribute.Bool(telemetry.UIPassthroughAttr, true))
	}

	if causeCtx.IsValid() {
		// link the vertex span to its causal span
		span.AddLink(trace.Link{SpanContext: causeCtx})
	}

	// convert cache prefixes into cached attribute (this is set deep inside
	// buildkit)
	spanName, cached := strings.CutPrefix(span.Name(), "load cache: ")
	if cached {
		span.SetName(spanName)
		span.SetAttributes(attribute.Bool(telemetry.CachedAttr, true))
	}

	span.SetAttributes(DAGAttributes(llbOp)...)
}

func (*SpanProcessor) OnEnd(sdktrace.ReadOnlySpan)      {}
func (*SpanProcessor) ForceFlush(context.Context) error { return nil }
func (*SpanProcessor) Shutdown(context.Context) error   { return nil }

func DAGAttributes(op *OpDAG) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		// TODO: consolidate? or do we need them to be distinct?
		// track the "DAG digest" in the same way that we track Dagger digests
		attribute.String(telemetry.DagDigestAttr, op.OpDigest.String()),
		// track the Buildkit effect-specific equivalent
		attribute.String(telemetry.EffectIDAttr, op.OpDigest.String()),
	}
	// track the inputs of the op
	// NOTE: this points to DagDigestAttr
	if len(op.Inputs) > 0 {
		inputs := make([]string, len(op.Inputs))
		for i, input := range op.Inputs {
			inputs[i] = input.OpDigest.String()
		}
		attrs = append(attrs, attribute.StringSlice(telemetry.DagInputsAttr, inputs))
	}
	// emit the deep dependencies of the op so the frontend can know that
	// they're completed without needing a span for each
	deps := opDeps(op, nil)
	if len(deps) > 0 {
		attrs = append(attrs,
			attribute.StringSlice(
				telemetry.EffectsCompletedAttr,
				deps,
			),
		)
	}
	return attrs
}

func opDeps(dag *OpDAG, seen map[digest.Digest]bool) []string {
	var doneEffects []string
	_ = dag.Walk(func(op *OpDAG) error {
		if op == dag {
			return nil
		}
		if seen != nil {
			if seen[*op.OpDigest] {
				return nil
			} else {
				seen[*op.OpDigest] = true
			}
		}
		doneEffects = append(doneEffects, op.OpDigest.String())
		return nil
	})
	return doneEffects
}
