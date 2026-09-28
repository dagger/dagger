package telemetry

import (
	"context"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// workloadSpanProcessor sends every engine span of the session, with only the
// fields in workloadSpanAttribute. Spans are not selected here: consumers decide
// which spans they need. Spans posted by SDK clients do not pass through this
// processor. Ordinary spans are never changed.
type workloadSpanProcessor struct {
	next     sdktrace.SpanProcessor
	resource *resource.Resource
}

// OnStart sends nothing: each span is sent once, when it ends. Work that never
// ends, for example because the engine stopped, has no span; its resource
// readings still carry its execution ID.
func (*workloadSpanProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (p *workloadSpanProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	var attrs []attribute.KeyValue
	for _, attr := range span.Attributes() {
		if workloadSpanAttribute(attr.Key) {
			attrs = append(attrs, attr)
		}
	}
	var links []sdktrace.Link
	for _, link := range span.Links() {
		var purpose string
		for _, attr := range link.Attributes {
			if string(attr.Key) == telemetry.LinkPurposeAttr {
				purpose = attr.Value.AsString()
			}
		}
		if purpose != "" && purpose != "cause" {
			continue
		}
		kept := sdktrace.Link{SpanContext: link.SpanContext.WithTraceState(trace.TraceState{})}
		if purpose != "" {
			kept.Attributes = []attribute.KeyValue{attribute.String(telemetry.LinkPurposeAttr, purpose)}
		}
		links = append(links, kept)
	}
	p.next.OnEnd(workloadSpan{
		name: span.Name(), sc: span.SpanContext().WithTraceState(trace.TraceState{}),
		parent: span.Parent().WithTraceState(trace.TraceState{}), start: span.StartTime(), end: span.EndTime(),
		attrs: attrs, links: links, status: sdktrace.Status{Code: span.Status().Code},
		scope: instrumentation.Scope{Name: span.InstrumentationScope().Name}, resource: p.resource,
	})
}

func (*workloadSpanProcessor) ForceFlush(context.Context) error { return nil }

func (*workloadSpanProcessor) Shutdown(context.Context) error { return nil }

func workloadSpanAttribute(key attribute.Key) bool {
	switch string(key) {
	case telemetry.DagDigestAttr, telemetry.CachedAttr, telemetry.PendingAttr, telemetry.UIInternalAttr,
		telemetryattrs.DagContentPreferredDigestAttr, telemetryattrs.DagPartialAttr, telemetryattrs.DagBlockedAttr,
		telemetryattrs.CacheContractAttr,
		telemetryattrs.CacheOutcomeAttr, telemetryattrs.CacheHitRouteAttr, telemetryattrs.CacheResultIDAttr,
		telemetryattrs.WcprofOpKindAttr, telemetryattrs.WcprofParentAttr,
		telemetryattrs.TelemetryOriginClientIDAttr, ExecutionIDAttr, ExecutionInternalAttr:
		return true
	default:
		return false
	}
}

// Immutable copies hold only the listed fields, not a reference to the
// original span and its potentially large payload. The embedded interface
// supplies the SDK's private method; all public methods are implemented here.
type workloadSpan struct {
	sdktrace.ReadOnlySpan
	name       string
	sc, parent trace.SpanContext
	start, end time.Time
	attrs      []attribute.KeyValue
	links      []sdktrace.Link
	status     sdktrace.Status
	scope      instrumentation.Scope
	resource   *resource.Resource
}

func (s workloadSpan) Name() string                                  { return s.name }
func (s workloadSpan) SpanContext() trace.SpanContext                { return s.sc }
func (s workloadSpan) Parent() trace.SpanContext                     { return s.parent }
func (workloadSpan) SpanKind() trace.SpanKind                        { return trace.SpanKindInternal }
func (s workloadSpan) StartTime() time.Time                          { return s.start }
func (s workloadSpan) EndTime() time.Time                            { return s.end }
func (s workloadSpan) Attributes() []attribute.KeyValue              { return s.attrs }
func (s workloadSpan) Links() []sdktrace.Link                        { return s.links }
func (workloadSpan) Events() []sdktrace.Event                        { return nil }
func (s workloadSpan) Status() sdktrace.Status                       { return s.status }
func (s workloadSpan) InstrumentationScope() instrumentation.Scope   { return s.scope }
func (s workloadSpan) InstrumentationLibrary() instrumentation.Scope { return s.scope }
func (s workloadSpan) Resource() *resource.Resource                  { return s.resource }
func (workloadSpan) DroppedAttributes() int                          { return 0 }
func (workloadSpan) DroppedLinks() int                               { return 0 }
func (workloadSpan) DroppedEvents() int                              { return 0 }
func (workloadSpan) ChildSpanCount() int                             { return 0 }
