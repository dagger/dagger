package telemetry

import (
	"context"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// SpansFromPB keeps scope metadata omitted by the current otel-go decoder.
func SpansFromPB(resources []*tracepb.ResourceSpans) []sdktrace.ReadOnlySpan {
	var spans []sdktrace.ReadOnlySpan
	for _, r := range resources {
		res := r.GetResource()
		if res == nil {
			res = &resourcepb.Resource{}
		}
		for _, s := range r.GetScopeSpans() {
			scope := instrumentation.Scope{Name: s.GetScope().GetName(), Version: s.GetScope().GetVersion(), SchemaURL: s.GetSchemaUrl(), Attributes: attribute.NewSet(telemetry.AttributesFromProto(s.GetScope().GetAttributes())...)}
			decoded := telemetry.SpansFromPB([]*tracepb.ResourceSpans{{Resource: res, SchemaUrl: r.GetSchemaUrl(), ScopeSpans: []*tracepb.ScopeSpans{s}}})
			for _, span := range decoded {
				spans = append(spans, spanWithScope{span, scope})
			}
		}
	}
	return spans
}

type spanWithScope struct {
	sdktrace.ReadOnlySpan
	scope instrumentation.Scope
}

func (s spanWithScope) InstrumentationScope() instrumentation.Scope   { return s.scope }
func (s spanWithScope) InstrumentationLibrary() instrumentation.Scope { return s.scope }

// Like otel-go's LiveSpanProcessor, but retain the original scope while making
// the start snapshot. This can use that processor once SnapshotSpan preserves
// scope attributes and schema URLs upstream.
type snapshotSpanProcessor struct{ sdktrace.SpanProcessor }

func (p *snapshotSpanProcessor) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	p.OnEnd(spanWithScope{telemetry.SnapshotSpan(span), span.InstrumentationScope()})
}
