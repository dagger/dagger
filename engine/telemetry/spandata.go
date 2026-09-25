package telemetry

import (
	"context"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// operationSpanProcessor projects engine-created spans without changing the
// records used by the CLI or Cloud. Selection happens before the shared queue;
// no call payload, event, exception text, or arbitrary resource is retained.
// Keep lazy/call-exec links and bypass omitted intermediate spans in the private
// parent graph. Deferred work can start after its parent ends, so retain bounded
// parent aliases for the session. Once that bound is full, export a minimal link
// instead of losing the relation. Ordinary span parentage is never changed.
type operationSpanProcessor struct {
	next     sdktrace.SpanProcessor
	resource *resource.Resource

	mu      sync.Mutex
	parents map[operationSpanKey]operationParent
	ended   map[operationSpanKey]trace.SpanContext
	closed  bool
}

const maxEndedOperationParents = 4096

type operationSpanKey struct {
	trace trace.TraceID
	span  trace.SpanID
}

type operationParent struct {
	parent, nearest trace.SpanContext
}

func operationKey(sc trace.SpanContext) operationSpanKey {
	return operationSpanKey{sc.TraceID(), sc.SpanID()}
}

func (p *operationSpanProcessor) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	parent := span.Parent()
	kind := operationKind(span)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	if p.parents == nil {
		p.parents = make(map[operationSpanKey]operationParent)
		p.ended = make(map[operationSpanKey]trace.SpanContext)
	}
	if previous, ok := p.parents[operationKey(parent)]; ok {
		parent = previous.nearest
	} else if nearest, ok := p.ended[operationKey(parent)]; ok {
		parent = nearest
	}
	nearest := parent
	if kind != "" {
		nearest = span.SpanContext()
	}
	p.parents[operationKey(span.SpanContext())] = operationParent{parent, nearest}
	p.mu.Unlock()
	p.export(span, parent, kind)
}

func (p *operationSpanProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	parent := span.Parent()
	kind := operationKind(span)
	link := false
	key := operationKey(span.SpanContext())
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	if saved, ok := p.parents[key]; ok {
		parent = saved.parent
		if kind == "" {
			if len(p.ended) < maxEndedOperationParents {
				p.ended[key] = saved.nearest
			} else {
				link = true
			}
		}
	}
	delete(p.parents, key)
	p.mu.Unlock()
	if link {
		p.next.OnEnd(operationSpan{
			name: "operation link", sc: span.SpanContext().WithTraceState(trace.TraceState{}),
			parent: parent.WithTraceState(trace.TraceState{}), start: span.StartTime(), end: span.EndTime(),
			attrs: []attribute.KeyValue{attribute.String(OperationKindAttr, "link")},
			scope: instrumentation.Scope{Name: "dagger.io/engine.operations"}, resource: p.resource,
		})
	}
	p.export(span, parent, kind)
}

func operationKind(span sdktrace.ReadOnlySpan) string {
	scope := span.InstrumentationScope().Name
	switch scope {
	case "dagger.io/core", "dagger.io/dagql", "dagger.io/engine.buildkit":
	default:
		return ""
	}
	var digest, kind string
	for _, attr := range span.Attributes() {
		switch string(attr.Key) {
		case telemetry.DagDigestAttr:
			digest = attr.Value.AsString()
		case telemetryattrs.WcprofOpKindAttr:
			kind = attr.Value.AsString()
		}
	}
	switch kind {
	case "lazy", "call_exec", "exec":
		return kind
	case "":
		if scope == "dagger.io/core" && digest != "" {
			return "call"
		}
	}
	return ""
}

func (p *operationSpanProcessor) export(span sdktrace.ReadOnlySpan, parent trace.SpanContext, kind string) {
	if kind == "" {
		return
	}
	var attrs []attribute.KeyValue
	for _, attr := range span.Attributes() {
		if operationAttribute(attr.Key) {
			attrs = append(attrs, attr)
		}
	}
	attrs = append(attrs, attribute.String(OperationKindAttr, kind))
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
	end := span.EndTime()
	if end.IsZero() {
		end = time.Unix(0, 0)
	}
	p.next.OnEnd(operationSpan{
		name: span.Name(), sc: span.SpanContext().WithTraceState(trace.TraceState{}),
		parent: parent.WithTraceState(trace.TraceState{}), start: span.StartTime(), end: end,
		attrs: attrs, links: links, status: sdktrace.Status{Code: span.Status().Code},
		scope: instrumentation.Scope{Name: span.InstrumentationScope().Name}, resource: p.resource,
	})
}

func (*operationSpanProcessor) ForceFlush(context.Context) error { return nil }

func (p *operationSpanProcessor) Shutdown(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.parents = nil
	p.ended = nil
	return nil
}

func operationAttribute(key attribute.Key) bool {
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

// Immutable snapshots hold only the selected fields, not a reference to the
// original span and its potentially large payload. The embedded interface
// supplies the SDK's private method; all public methods are implemented here.
type operationSpan struct {
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

func (s operationSpan) Name() string                                  { return s.name }
func (s operationSpan) SpanContext() trace.SpanContext                { return s.sc }
func (s operationSpan) Parent() trace.SpanContext                     { return s.parent }
func (operationSpan) SpanKind() trace.SpanKind                        { return trace.SpanKindInternal }
func (s operationSpan) StartTime() time.Time                          { return s.start }
func (s operationSpan) EndTime() time.Time                            { return s.end }
func (s operationSpan) Attributes() []attribute.KeyValue              { return s.attrs }
func (s operationSpan) Links() []sdktrace.Link                        { return s.links }
func (operationSpan) Events() []sdktrace.Event                        { return nil }
func (s operationSpan) Status() sdktrace.Status                       { return s.status }
func (s operationSpan) InstrumentationScope() instrumentation.Scope   { return s.scope }
func (s operationSpan) InstrumentationLibrary() instrumentation.Scope { return s.scope }
func (s operationSpan) Resource() *resource.Resource                  { return s.resource }
func (operationSpan) DroppedAttributes() int                          { return 0 }
func (operationSpan) DroppedLinks() int                               { return 0 }
func (operationSpan) DroppedEvents() int                              { return 0 }
func (operationSpan) ChildSpanCount() int                             { return 0 }
