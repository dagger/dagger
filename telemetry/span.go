package telemetry

import (
	"time"

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

// Encapsulate can be applied to a span to indicate that this span should
// collapse its children by default.
func Encapsulate() trace.SpanStartOption {
	return trace.WithAttributes(attribute.Bool(UIEncapsulateAttr, true))
}

// Internal can be applied to a span to indicate that this span should not be
// shown to the user by default.
func Internal() trace.SpanStartOption {
	return trace.WithAttributes(attribute.Bool(UIInternalAttr, true))
}

// End is a helper to end a span with an error if the function returns an error.
//
// It is optimized for use as a defer one-liner with a function that has a
// named error return value, conventionally `rerr`.
//
//	defer telemetry.End(span, func() error { return rerr })
func End(span trace.Span, fn func() error) {
	if err := fn(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// SpansFromProto transforms a slice of OTLP ResourceSpans into a slice of
// ReadOnlySpans.
func SpansFromProto(sdl []*otlptracev1.ResourceSpans) []sdktrace.ReadOnlySpan {
	if len(sdl) == 0 {
		return nil
	}

	var out []sdktrace.ReadOnlySpan

	for _, sd := range sdl {
		if sd == nil {
			continue
		}

		for _, sdi := range sd.ScopeSpans {
			if sdi == nil {
				continue
			}
			sda := make([]sdktrace.ReadOnlySpan, 0, len(sdi.Spans))
			for _, s := range sdi.Spans {
				if s == nil {
					continue
				}
				sda = append(sda, &readOnlySpan{
					pb:        s,
					is:        sdi.Scope,
					resource:  sd.Resource,
					schemaURL: sd.SchemaUrl,
				})
			}
			out = append(out, sda...)
		}
	}

	return out
}

type readOnlySpan struct {
	// Embed the interface to implement the private method.
	sdktrace.ReadOnlySpan

	pb        *otlptracev1.Span
	is        *otlpcommonv1.InstrumentationScope
	resource  *otlpresourcev1.Resource
	schemaURL string
}

func (s *readOnlySpan) Name() string {
	return s.pb.Name
}

func (s *readOnlySpan) SpanContext() trace.SpanContext {
	var tid trace.TraceID
	copy(tid[:], s.pb.TraceId)
	var sid trace.SpanID
	copy(sid[:], s.pb.SpanId)

	st, _ := trace.ParseTraceState(s.pb.TraceState)

	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceState: st,
		TraceFlags: trace.FlagsSampled,
	})
}

func (s *readOnlySpan) Parent() trace.SpanContext {
	if len(s.pb.ParentSpanId) == 0 {
		return trace.SpanContext{}
	}
	var tid trace.TraceID
	copy(tid[:], s.pb.TraceId)
	var psid trace.SpanID
	copy(psid[:], s.pb.ParentSpanId)
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid,
		SpanID:  psid,
	})
}

func (s *readOnlySpan) SpanKind() trace.SpanKind {
	return spanKind(s.pb.Kind)
}

func (s *readOnlySpan) StartTime() time.Time {
	return time.Unix(0, int64(s.pb.StartTimeUnixNano))
}

func (s *readOnlySpan) EndTime() time.Time {
	return time.Unix(0, int64(s.pb.EndTimeUnixNano))
}

func (s *readOnlySpan) Attributes() []attribute.KeyValue {
	return AttributesFromProto(s.pb.Attributes)
}

func (s *readOnlySpan) Links() []sdktrace.Link {
	return links(s.pb.Links)
}

func (s *readOnlySpan) Events() []sdktrace.Event {
	return spanEvents(s.pb.Events)
}

func (s *readOnlySpan) Status() sdktrace.Status {
	return sdktrace.Status{
		Code:        statusCode(s.pb.Status),
		Description: s.pb.Status.GetMessage(),
	}
}

func (s *readOnlySpan) InstrumentationScope() instrumentation.Scope {
	return instrumentationScope(s.is)
}

// Deprecated: use InstrumentationScope.
func (s *readOnlySpan) InstrumentationLibrary() instrumentation.Library {
	return s.InstrumentationScope()
}

// Resource returns information about the entity that produced the span.
func (s *readOnlySpan) Resource() *resource.Resource {
	if s.resource == nil {
		return nil
	}
	if s.schemaURL != "" {
		return resource.NewWithAttributes(s.schemaURL, AttributesFromProto(s.resource.Attributes)...)
	}
	return resource.NewSchemaless(AttributesFromProto(s.resource.Attributes)...)
}

// DroppedAttributes returns the number of attributes dropped by the span
// due to limits being reached.
func (s *readOnlySpan) DroppedAttributes() int {
	return int(s.pb.DroppedAttributesCount)
}

// DroppedLinks returns the number of links dropped by the span due to
// limits being reached.
func (s *readOnlySpan) DroppedLinks() int {
	return int(s.pb.DroppedLinksCount)
}

// DroppedEvents returns the number of events dropped by the span due to
// limits being reached.
func (s *readOnlySpan) DroppedEvents() int {
	return int(s.pb.DroppedEventsCount)
}

// ChildSpanCount returns the count of spans that consider the span a
// direct parent.
func (s *readOnlySpan) ChildSpanCount() int {
	return 0
}

var _ sdktrace.ReadOnlySpan = &readOnlySpan{}

// status transform a OTLP span status into span code.
func statusCode(st *otlptracev1.Status) codes.Code {
	if st == nil {
		return codes.Unset
	}
	switch st.Code {
	case otlptracev1.Status_STATUS_CODE_ERROR:
		return codes.Error
	default:
		return codes.Ok
	}
}

// links transforms OTLP span links to span Links.
func links(links []*otlptracev1.Span_Link) []sdktrace.Link {
	if len(links) == 0 {
		return nil
	}

	sl := make([]sdktrace.Link, 0, len(links))
	for _, otLink := range links {
		if otLink == nil {
			continue
		}
		// This redefinition is necessary to prevent otLink.*ID[:] copies
		// being reused -- in short we need a new otLink per iteration.
		otLink := otLink

		var tid trace.TraceID
		copy(tid[:], otLink.TraceId)
		var sid trace.SpanID
		copy(sid[:], otLink.SpanId)

		sctx := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: tid,
			SpanID:  sid,
		})

		sl = append(sl, sdktrace.Link{
			SpanContext: sctx,
			Attributes:  AttributesFromProto(otLink.Attributes),
		})
	}
	return sl
}

// spanEvents transforms OTLP span events to span Events.
func spanEvents(es []*otlptracev1.Span_Event) []sdktrace.Event {
	if len(es) == 0 {
		return nil
	}

	evCount := len(es)
	events := make([]sdktrace.Event, 0, evCount)
	messageEvents := 0

	// Transform message events
	for _, e := range es {
		if e == nil {
			continue
		}
		messageEvents++
		events = append(events,
			sdktrace.Event{
				Name:                  e.Name,
				Time:                  time.Unix(0, int64(e.TimeUnixNano)),
				Attributes:            AttributesFromProto(e.Attributes),
				DroppedAttributeCount: int(e.DroppedAttributesCount),
			},
		)
	}

	return events
}

// spanKind transforms a an OTLP span kind to SpanKind.
func spanKind(kind otlptracev1.Span_SpanKind) trace.SpanKind {
	switch kind {
	case otlptracev1.Span_SPAN_KIND_INTERNAL:
		return trace.SpanKindInternal
	case otlptracev1.Span_SPAN_KIND_CLIENT:
		return trace.SpanKindClient
	case otlptracev1.Span_SPAN_KIND_SERVER:
		return trace.SpanKindServer
	case otlptracev1.Span_SPAN_KIND_PRODUCER:
		return trace.SpanKindProducer
	case otlptracev1.Span_SPAN_KIND_CONSUMER:
		return trace.SpanKindConsumer
	default:
		return trace.SpanKindUnspecified
	}
}

// AttributesFromProto transforms a slice of OTLP attribute key-values into a slice of KeyValues
func AttributesFromProto(attrs []*otlpcommonv1.KeyValue) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}

	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		if a == nil {
			continue
		}
		kv := attribute.KeyValue{
			Key:   attribute.Key(a.Key),
			Value: toValue(a.Value),
		}
		out = append(out, kv)
	}
	return out
}

func toValue(v *otlpcommonv1.AnyValue) attribute.Value {
	switch vv := v.Value.(type) {
	case *otlpcommonv1.AnyValue_BoolValue:
		return attribute.BoolValue(vv.BoolValue)
	case *otlpcommonv1.AnyValue_IntValue:
		return attribute.Int64Value(vv.IntValue)
	case *otlpcommonv1.AnyValue_DoubleValue:
		return attribute.Float64Value(vv.DoubleValue)
	case *otlpcommonv1.AnyValue_StringValue:
		return attribute.StringValue(vv.StringValue)
	case *otlpcommonv1.AnyValue_ArrayValue:
		return arrayValues(vv.ArrayValue.Values)
	default:
		return attribute.StringValue("INVALID")
	}
}

func boolArray(kv []*otlpcommonv1.AnyValue) attribute.Value {
	arr := make([]bool, len(kv))
	for i, v := range kv {
		if v != nil {
			arr[i] = v.GetBoolValue()
		}
	}
	return attribute.BoolSliceValue(arr)
}

func intArray(kv []*otlpcommonv1.AnyValue) attribute.Value {
	arr := make([]int64, len(kv))
	for i, v := range kv {
		if v != nil {
			arr[i] = v.GetIntValue()
		}
	}
	return attribute.Int64SliceValue(arr)
}

func doubleArray(kv []*otlpcommonv1.AnyValue) attribute.Value {
	arr := make([]float64, len(kv))
	for i, v := range kv {
		if v != nil {
			arr[i] = v.GetDoubleValue()
		}
	}
	return attribute.Float64SliceValue(arr)
}

func stringArray(kv []*otlpcommonv1.AnyValue) attribute.Value {
	arr := make([]string, len(kv))
	for i, v := range kv {
		if v != nil {
			arr[i] = v.GetStringValue()
		}
	}
	return attribute.StringSliceValue(arr)
}

func arrayValues(kv []*otlpcommonv1.AnyValue) attribute.Value {
	if len(kv) == 0 || kv[0] == nil {
		return attribute.StringSliceValue([]string{})
	}

	switch kv[0].Value.(type) {
	case *otlpcommonv1.AnyValue_BoolValue:
		return boolArray(kv)
	case *otlpcommonv1.AnyValue_IntValue:
		return intArray(kv)
	case *otlpcommonv1.AnyValue_DoubleValue:
		return doubleArray(kv)
	case *otlpcommonv1.AnyValue_StringValue:
		return stringArray(kv)
	default:
		return attribute.StringSliceValue([]string{})
	}
}

func instrumentationScope(is *otlpcommonv1.InstrumentationScope) instrumentation.Scope {
	if is == nil {
		return instrumentation.Scope{}
	}
	return instrumentation.Scope{
		Name:    is.Name,
		Version: is.Version,
	}
}
