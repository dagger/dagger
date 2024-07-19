package clientdb

import (
	"encoding/json"
	"time"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func (span *Span) ReadOnly() sdktrace.ReadOnlySpan {
	return &readOnlySpan{
		DB: span,
	}
}

type readOnlySpan struct {
	// Have to embed the interface to get private(). Goofy.
	sdktrace.ReadOnlySpan

	DB *Span
}

var _ sdktrace.ReadOnlySpan = (*readOnlySpan)(nil)

// SpanContext returns the span context
func (ros *readOnlySpan) SpanContext() trace.SpanContext {
	tid, _ := trace.TraceIDFromHex(ros.DB.TraceID)
	sid, _ := trace.SpanIDFromHex(ros.DB.SpanID)
	ts, _ := trace.ParseTraceState(ros.DB.TraceState)
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.TraceFlags(ros.DB.Flags),
		TraceState: ts,
	})
}

// Parent returns the parent span context
func (ros *readOnlySpan) Parent() trace.SpanContext {
	if ros.DB.ParentSpanID.Valid {
		tid, _ := trace.TraceIDFromHex(ros.DB.TraceID)
		sid, _ := trace.SpanIDFromHex(ros.DB.ParentSpanID.String)
		return trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: tid,
			SpanID:  sid,
		})
	}
	return trace.SpanContext{}
}

// SpanKind returns the kind of the span
func (ros *readOnlySpan) SpanKind() trace.SpanKind {
	switch ros.DB.Kind {
	case "internal":
		return trace.SpanKindInternal
	case "server":
		return trace.SpanKindServer
	case "client":
		return trace.SpanKindClient
	case "producer":
		return trace.SpanKindProducer
	case "consumer":
		return trace.SpanKindConsumer
	default:
		return trace.SpanKindUnspecified
	}
}

// Name returns the name of the span
func (ros *readOnlySpan) Name() string {
	return ros.DB.Name
}

// StartTime returns the start time of the span
func (ros *readOnlySpan) StartTime() time.Time {
	return time.Unix(0, ros.DB.StartTime)
}

// EndTime returns the end time of the span
func (ros *readOnlySpan) EndTime() time.Time {
	if ros.DB.EndTime.Valid {
		return time.Unix(0, ros.DB.EndTime.Int64)
	}
	return time.Time{}
}

// Attributes returns the attributes of the span
func (ros *readOnlySpan) Attributes() []attribute.KeyValue {
	var attrs []*otlpcommonv1.KeyValue
	if err := json.Unmarshal(ros.DB.Attributes, &attrs); err != nil {
		return nil
	}
	return telemetry.AttributesFromProto(attrs)
}

// Links returns the links of the span
func (ros *readOnlySpan) Links() []sdktrace.Link {
	var links []*otlptracev1.Span_Link
	if err := json.Unmarshal(ros.DB.Attributes, &links); err != nil {
		return nil
	}
	return telemetry.SpanLinksFromPB(links)
}

// Events returns the events of the span
func (ros *readOnlySpan) Events() []sdktrace.Event {
	var events []*otlptracev1.Span_Event
	if err := json.Unmarshal(ros.DB.Attributes, &events); err != nil {
		return nil
	}
	return telemetry.SpanEventsFromPB(events)
}

// Status returns the status of the span
func (ros *readOnlySpan) Status() sdktrace.Status {
	return sdktrace.Status{
		Code:        codes.Code(ros.DB.StatusCode),
		Description: ros.DB.StatusMessage,
	}
}

// DroppedAttributes returns the count of dropped attributes
func (ros *readOnlySpan) DroppedAttributes() int {
	return int(ros.DB.DroppedAttributesCount)
}

// DroppedLinks returns the count of dropped links
func (ros *readOnlySpan) DroppedLinks() int {
	return int(ros.DB.DroppedLinksCount)
}

// DroppedEvents returns the count of dropped events
func (ros *readOnlySpan) DroppedEvents() int {
	return int(ros.DB.DroppedEventsCount)
}

// ChildSpanCount returns the count of child spans
func (ros *readOnlySpan) ChildSpanCount() int {
	return 0 // Not stored in this example
}

// Resource returns the resource associated with the span
func (ros *readOnlySpan) Resource() *resource.Resource {
	var res *otlpresourcev1.Resource
	if err := json.Unmarshal(ros.DB.Resource, &res); err != nil {
		return nil
	}
	return resource.NewSchemaless(telemetry.AttributesFromProto(res.Attributes)...)
}

// InstrumentationLibrary returns the instrumentation library
func (ros *readOnlySpan) InstrumentationLibrary() instrumentation.Library {
	return ros.InstrumentationScope()
}

// InstrumentationScope returns the instrumentation scope
func (ros *readOnlySpan) InstrumentationScope() instrumentation.Scope {
	// Assuming instrumentation scope is stored as JSON and needs to be parsed
	var is *otlpcommonv1.InstrumentationScope
	if err := json.Unmarshal(ros.DB.InstrumentationScope, &is); err != nil {
		return instrumentation.Scope{}
	}
	return telemetry.InstrumentationScopeFromPB(is)
}

// Ended returns whether the span has ended
func (ros *readOnlySpan) Ended() bool {
	return ros.DB.EndTime.Valid
}

// type ReadOnlySpan interface {
// 	// Name returns the name of the span.
// 	Name() string
// 	// SpanContext returns the unique SpanContext that identifies the span.
// 	SpanContext() trace.SpanContext
// 	// Parent returns the unique SpanContext that identifies the parent of the
// 	// span if one exists. If the span has no parent the returned SpanContext
// 	// will be invalid.
// 	Parent() trace.SpanContext
// 	// SpanKind returns the role the span plays in a Trace.
// 	SpanKind() trace.SpanKind
// 	// StartTime returns the time the span started recording.
// 	StartTime() time.Time
// 	// EndTime returns the time the span stopped recording. It will be zero if
// 	// the span has not ended.
// 	EndTime() time.Time
// 	// Attributes returns the defining attributes of the span.
// 	// The order of the returned attributes is not guaranteed to be stable across invocations.
// 	Attributes() []attribute.KeyValue
// 	// Links returns all the links the span has to other spans.
// 	Links() []Link
// 	// Events returns all the events that occurred within in the spans
// 	// lifetime.
// 	Events() []Event
// 	// Status returns the spans status.
// 	Status() Status
// 	// InstrumentationScope returns information about the instrumentation
// 	// scope that created the span.
// 	InstrumentationScope() instrumentation.Scope
// 	// InstrumentationLibrary returns information about the instrumentation
// 	// library that created the span.
// 	// Deprecated: please use InstrumentationScope instead.
// 	InstrumentationLibrary() instrumentation.Library
// 	// Resource returns information about the entity that produced the span.
// 	Resource() *resource.Resource
// 	// DroppedAttributes returns the number of attributes dropped by the span
// 	// due to limits being reached.
// 	DroppedAttributes() int
// 	// DroppedLinks returns the number of links dropped by the span due to
// 	// limits being reached.
// 	DroppedLinks() int
// 	// DroppedEvents returns the number of events dropped by the span due to
// 	// limits being reached.
// 	DroppedEvents() int
// 	// ChildSpanCount returns the count of spans that consider the span a
// 	// direct parent.
// 	ChildSpanCount() int
