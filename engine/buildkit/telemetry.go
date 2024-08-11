package buildkit

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
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

func SpanContextFromDescription(desc map[string]string) trace.SpanContext {
	return trace.SpanContextFromContext(
		telemetry.Propagator.Extract(context.Background(), propagation.MapCarrier(desc)),
	)
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

	cfg := trace.NewSpanStartConfig(opts...)
	for _, attr := range cfg.Attributes() {
		if attr.Key != "vertex" {
			continue
		}

		dgst := digest.Digest(attr.Value.AsString())

		t.provider.client.opsmu.Lock()
		llbop, ok := t.provider.client.ops[dgst]
		if !ok {
			t.provider.client.opsmu.Unlock()
			continue
		}
		llbop.seen = true
		t.provider.client.opsmu.Unlock()

		causeCtx := SpanContextFromDescription(llbop.Meta.Description)
		if causeCtx.IsValid() {
			opts = append(opts, trace.WithLinks(trace.Link{
				SpanContext: causeCtx,
				// Attributes:  []attribute.KeyValue{},
			}))
		}

		// TODO: bring this back? not sure what it does
		// if strings.HasPrefix(spanName, "load cache: ") {
		// 	if ok {
		// 		for _, input := range llbop.Def.Inputs {
		// 			t.walk(ctx, input.Digest, func(llbop *op) {
		// 				_, span := t.tracer.Start(ctx, t.name(llbop.Digest), trace.WithAttributes(
		// 					attribute.Bool("buildkit", true),
		// 					attribute.Bool("dagger.io/dag.virtual", true),
		// 					attribute.Bool(telemetry.CachedAttr, true),
		// 					attribute.String("vertex", string(llbop.Digest)),
		// 				))
		// 				span.End()
		// 			})
		// 		}
		// 	}
		// 	break
		// }
	}

	// Start the span, and make sure we return a span that has the provider.
	ctx, span := t.tracer.Start(ctx, spanName, opts...)
	newSpan := buildkitSpan{Span: span, tp: t.provider}
	return trace.ContextWithSpan(ctx, newSpan), newSpan
}

func (t *buildkitTracer) walk(ctx context.Context, digest digest.Digest, cb func(*op)) {
	t.provider.client.opsmu.RLock()
	op, ok := t.provider.client.ops[digest]
	seen := op.seen
	t.provider.client.opsmu.RUnlock()
	if !ok || seen {
		return
	}

	for _, input := range op.Def.Inputs {
		t.walk(ctx, input.Digest, cb)
	}

	cb(op)

	t.provider.client.opsmu.Lock()
	op.seen = true
	t.provider.client.opsmu.Unlock()
}

func (t *buildkitTracer) name(d digest.Digest) string {
	t.provider.client.opsmu.RLock()
	op := t.provider.client.ops[d]
	t.provider.client.opsmu.RUnlock()

	name, ok := op.Meta.Description["llb.customname"]
	if ok {
		return name
	}

	name, err := llbOpName(op, t.name)
	if err != nil {
		panic(err)
	}
	return name
}

func llbOpName(llbop *op, basename func(digest.Digest) string) (string, error) {
	switch op := llbop.Def.Op.(type) {
	case *pb.Op_Source:
		return op.Source.Identifier, nil
	case *pb.Op_Exec:
		return strings.Join(op.Exec.Meta.Args, " "), nil
	case *pb.Op_File:
		return fileOpName(op.File.Actions), nil
	case *pb.Op_Build:
		return "build", nil
	case *pb.Op_Merge:
		subnames := make([]string, len(llbop.Def.Inputs))
		for i, inp := range llbop.Def.Inputs {
			subvtx := basename(inp.Digest)
			subnames[i] = subvtx
		}
		return "merge " + fmt.Sprintf("(%s)", strings.Join(subnames, ", ")), nil
	case *pb.Op_Diff:
		var lowerName string
		if op.Diff.Lower.Input == -1 {
			lowerName = "scratch"
		} else {
			lowerVtx := basename(llbop.Def.Inputs[op.Diff.Lower.Input].Digest)
			lowerName = fmt.Sprintf("(%s)", lowerVtx)
		}
		var upperName string
		if op.Diff.Upper.Input == -1 {
			upperName = "scratch"
		} else {
			upperVtx := basename(llbop.Def.Inputs[op.Diff.Upper.Input].Digest)
			upperName = fmt.Sprintf("(%s)", upperVtx)
		}
		return "diff " + lowerName + " -> " + upperName, nil
	default:
		return "unknown", nil
	}
}

func fileOpName(actions []*pb.FileAction) string {
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		switch a := action.Action.(type) {
		case *pb.FileAction_Mkdir:
			names = append(names, fmt.Sprintf("mkdir %s", a.Mkdir.Path))
		case *pb.FileAction_Mkfile:
			names = append(names, fmt.Sprintf("mkfile %s", a.Mkfile.Path))
		case *pb.FileAction_Rm:
			names = append(names, fmt.Sprintf("rm %s", a.Rm.Path))
		case *pb.FileAction_Copy:
			names = append(names, fmt.Sprintf("copy %s %s", a.Copy.Src, a.Copy.Dest))
		}
	}

	return strings.Join(names, ", ")
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

	// convert cache prefixes into cached attribute (this is set deep inside buildkit)
	spanName, cached := strings.CutPrefix(spanName, "load cache: ")
	if cached {
		span.SetName(spanName)
		attrs = append(attrs, attribute.Bool(telemetry.CachedAttr, true))
	}

	// convert [internal] prefix into internal attribute
	if rest, ok := strings.CutPrefix(spanName, InternalPrefix); ok {
		span.SetName(rest)
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
