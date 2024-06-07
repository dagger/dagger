package telemetry

import (
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func SnapshotSpan(span sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	return SpansFromPB(SpansToPB([]sdktrace.ReadOnlySpan{span}))[0]
}

func LogsToPB(sdl []sdklog.Record) []*otlplogsv1.ResourceLogs {
	if len(sdl) == 0 {
		return nil
	}

	rsm := make(map[attribute.Distinct]*otlplogsv1.ResourceLogs)

	type key struct {
		r  attribute.Distinct
		is instrumentation.Scope
	}
	ssm := make(map[key]*otlplogsv1.ScopeLogs)

	var resources int
	for _, sd := range sdl {
		res := sd.Resource()
		rKey := res.Equivalent()
		k := key{
			r:  rKey,
			is: sd.InstrumentationScope(),
		}
		scopeLog, iOk := ssm[k]
		if !iOk {
			// Either the resource or instrumentation scope were unknown.
			scopeLog = &otlplogsv1.ScopeLogs{
				Scope:      InstrumentationScope(sd.InstrumentationScope()),
				LogRecords: []*otlplogsv1.LogRecord{},
				SchemaUrl:  sd.InstrumentationScope().SchemaURL,
			}
		}
		scopeLog.LogRecords = append(scopeLog.LogRecords, logRecord(sd))
		ssm[k] = scopeLog

		rs, rOk := rsm[rKey]
		if !rOk {
			resources++
			// The resource was unknown.
			rs = &otlplogsv1.ResourceLogs{
				Resource:  Resource(res),
				ScopeLogs: []*otlplogsv1.ScopeLogs{scopeLog},
				SchemaUrl: res.SchemaURL(),
			}
			rsm[rKey] = rs
			continue
		}

		// The resource has been seen before. Check if the instrumentation
		// library lookup was unknown because if so we need to add it to the
		// ResourceSpans. Otherwise, the instrumentation library has already
		// been seen and the append we did above will be included it in the
		// ScopeSpans reference.
		if !iOk {
			rs.ScopeLogs = append(rs.ScopeLogs, scopeLog)
		}
	}

	// Transform the categorized map into a slice
	rss := make([]*otlplogsv1.ResourceLogs, 0, resources)
	for _, rs := range rsm {
		rss = append(rss, rs)
	}
	return rss
}

func InstrumentationScope(il instrumentation.Scope) *otlpcommonv1.InstrumentationScope {
	if il == (instrumentation.Scope{}) {
		return nil
	}
	return &otlpcommonv1.InstrumentationScope{
		Name:    il.Name,
		Version: il.Version,
	}
}

// span transforms a Span into an OTLP span.
func logRecord(l sdklog.Record) *otlplogsv1.LogRecord {
	attrs := []*otlpcommonv1.KeyValue{}
	l.WalkAttributes(func(kv log.KeyValue) bool {
		attrs = append(attrs, &otlpcommonv1.KeyValue{
			Key:   kv.Key,
			Value: logValueToPB(kv.Value),
		})
		return true
	})

	tid, sid := l.TraceID(), l.SpanID()
	s := &otlplogsv1.LogRecord{
		TimeUnixNano:   uint64(l.Timestamp().UnixNano()),
		SeverityNumber: otlplogsv1.SeverityNumber(l.Severity()),
		SeverityText:   l.SeverityText(),
		Body:           logValueToPB(l.Body()),
		Attributes:     attrs,
		// DroppedAttributesCount: 0,
		// Flags: 0,
		TraceId: tid[:],
		SpanId:  sid[:],
	}

	return s
}

// Resource transforms a Resource into an OTLP Resource.
func Resource(r resource.Resource) *otlpresourcev1.Resource {
	return &otlpresourcev1.Resource{Attributes: resourceAttributes(r)}
}

// Resource transforms a Resource into an OTLP Resource.
func ResourcePtr(r *resource.Resource) *otlpresourcev1.Resource {
	if r == nil {
		return nil
	}
	return &otlpresourcev1.Resource{Attributes: resourceAttributes(*r)}
}

func resourceAttributes(res resource.Resource) []*otlpcommonv1.KeyValue {
	return iterator(res.Iter())
}

func iterator(iter attribute.Iterator) []*otlpcommonv1.KeyValue {
	l := iter.Len()
	if l == 0 {
		return nil
	}

	out := make([]*otlpcommonv1.KeyValue, 0, l)
	for iter.Next() {
		out = append(out, keyValueToPB(iter.Attribute()))
	}
	return out
}

func keyValueToPB(kv attribute.KeyValue) *otlpcommonv1.KeyValue {
	return &otlpcommonv1.KeyValue{Key: string(kv.Key), Value: value(kv.Value)}
}

// value transforms an attribute value into an OTLP AnyValue.
func value(v attribute.Value) *otlpcommonv1.AnyValue {
	av := new(otlpcommonv1.AnyValue)
	switch v.Type() {
	case attribute.BOOL:
		av.Value = &otlpcommonv1.AnyValue_BoolValue{
			BoolValue: v.AsBool(),
		}
	case attribute.BOOLSLICE:
		av.Value = &otlpcommonv1.AnyValue_ArrayValue{
			ArrayValue: &otlpcommonv1.ArrayValue{
				Values: boolSliceValues(v.AsBoolSlice()),
			},
		}
	case attribute.INT64:
		av.Value = &otlpcommonv1.AnyValue_IntValue{
			IntValue: v.AsInt64(),
		}
	case attribute.INT64SLICE:
		av.Value = &otlpcommonv1.AnyValue_ArrayValue{
			ArrayValue: &otlpcommonv1.ArrayValue{
				Values: int64SliceValues(v.AsInt64Slice()),
			},
		}
	case attribute.FLOAT64:
		av.Value = &otlpcommonv1.AnyValue_DoubleValue{
			DoubleValue: v.AsFloat64(),
		}
	case attribute.FLOAT64SLICE:
		av.Value = &otlpcommonv1.AnyValue_ArrayValue{
			ArrayValue: &otlpcommonv1.ArrayValue{
				Values: float64SliceValues(v.AsFloat64Slice()),
			},
		}
	case attribute.STRING:
		av.Value = &otlpcommonv1.AnyValue_StringValue{
			StringValue: v.AsString(),
		}
	case attribute.STRINGSLICE:
		av.Value = &otlpcommonv1.AnyValue_ArrayValue{
			ArrayValue: &otlpcommonv1.ArrayValue{
				Values: stringSliceValues(v.AsStringSlice()),
			},
		}
	default:
		av.Value = &otlpcommonv1.AnyValue_StringValue{
			StringValue: "INVALID",
		}
	}
	return av
}

func boolSliceValues(vals []bool) []*otlpcommonv1.AnyValue {
	converted := make([]*otlpcommonv1.AnyValue, len(vals))
	for i, v := range vals {
		converted[i] = &otlpcommonv1.AnyValue{
			Value: &otlpcommonv1.AnyValue_BoolValue{
				BoolValue: v,
			},
		}
	}
	return converted
}

func int64SliceValues(vals []int64) []*otlpcommonv1.AnyValue {
	converted := make([]*otlpcommonv1.AnyValue, len(vals))
	for i, v := range vals {
		converted[i] = &otlpcommonv1.AnyValue{
			Value: &otlpcommonv1.AnyValue_IntValue{
				IntValue: v,
			},
		}
	}
	return converted
}

func float64SliceValues(vals []float64) []*otlpcommonv1.AnyValue {
	converted := make([]*otlpcommonv1.AnyValue, len(vals))
	for i, v := range vals {
		converted[i] = &otlpcommonv1.AnyValue{
			Value: &otlpcommonv1.AnyValue_DoubleValue{
				DoubleValue: v,
			},
		}
	}
	return converted
}

func stringSliceValues(vals []string) []*otlpcommonv1.AnyValue {
	converted := make([]*otlpcommonv1.AnyValue, len(vals))
	for i, v := range vals {
		converted[i] = &otlpcommonv1.AnyValue{
			Value: &otlpcommonv1.AnyValue_StringValue{
				StringValue: v,
			},
		}
	}
	return converted
}

// SpansFromPB transforms a slice of OTLP ResourceSpans into a slice of
// ReadOnlySpans.
func SpansFromPB(sdl []*otlptracev1.ResourceSpans) []sdktrace.ReadOnlySpan {
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

// SpansToPB transforms a slice of OpenTelemetry spans into a slice of OTLP
// ResourceSpans.
func SpansToPB(sdl []sdktrace.ReadOnlySpan) []*otlptracev1.ResourceSpans {
	if len(sdl) == 0 {
		return nil
	}

	rsm := make(map[attribute.Distinct]*otlptracev1.ResourceSpans)

	type key struct {
		r  attribute.Distinct
		is instrumentation.Scope
	}
	ssm := make(map[key]*otlptracev1.ScopeSpans)

	var resources int
	for _, sd := range sdl {
		if sd == nil {
			continue
		}

		rKey := sd.Resource().Equivalent()
		k := key{
			r:  rKey,
			is: sd.InstrumentationScope(),
		}
		scopeSpan, iOk := ssm[k]
		if !iOk {
			// Either the resource or instrumentation scope were unknown.
			scopeSpan = &otlptracev1.ScopeSpans{
				Scope:     InstrumentationScope(sd.InstrumentationScope()),
				Spans:     []*otlptracev1.Span{},
				SchemaUrl: sd.InstrumentationScope().SchemaURL,
			}
		}
		scopeSpan.Spans = append(scopeSpan.Spans, spanToPB(sd))
		ssm[k] = scopeSpan

		rs, rOk := rsm[rKey]
		if !rOk {
			resources++
			// The resource was unknown.
			rs = &otlptracev1.ResourceSpans{
				Resource:   ResourcePtr(sd.Resource()),
				ScopeSpans: []*otlptracev1.ScopeSpans{scopeSpan},
				SchemaUrl:  sd.Resource().SchemaURL(),
			}
			rsm[rKey] = rs
			continue
		}

		// The resource has been seen before. Check if the instrumentation
		// library lookup was unknown because if so we need to add it to the
		// ResourceSpans. Otherwise, the instrumentation library has already
		// been seen and the append we did above will be included it in the
		// ScopeSpans reference.
		if !iOk {
			rs.ScopeSpans = append(rs.ScopeSpans, scopeSpan)
		}
	}

	// Transform the categorized map into a slice
	rss := make([]*otlptracev1.ResourceSpans, 0, resources)
	for _, rs := range rsm {
		rss = append(rss, rs)
	}
	return rss
}

// spanToPB transforms a Span into an OTLP span.
func spanToPB(sd sdktrace.ReadOnlySpan) *otlptracev1.Span {
	if sd == nil {
		return nil
	}

	tid := sd.SpanContext().TraceID()
	sid := sd.SpanContext().SpanID()

	s := &otlptracev1.Span{
		TraceId:                tid[:],
		SpanId:                 sid[:],
		TraceState:             sd.SpanContext().TraceState().String(),
		Status:                 status(sd.Status().Code, sd.Status().Description),
		StartTimeUnixNano:      uint64(sd.StartTime().UnixNano()),
		EndTimeUnixNano:        uint64(sd.EndTime().UnixNano()),
		Links:                  linksToPB(sd.Links()),
		Kind:                   spanKindToPB(sd.SpanKind()),
		Name:                   sd.Name(),
		Attributes:             KeyValues(sd.Attributes()),
		Events:                 spanEventsToPB(sd.Events()),
		DroppedAttributesCount: uint32(sd.DroppedAttributes()),
		DroppedEventsCount:     uint32(sd.DroppedEvents()),
		DroppedLinksCount:      uint32(sd.DroppedLinks()),
	}

	if psid := sd.Parent().SpanID(); psid.IsValid() {
		s.ParentSpanId = psid[:]
	}
	s.Flags = buildSpanFlags(sd.Parent())

	return s
}

// status transform a span code and message into an OTLP span status.
func status(status codes.Code, message string) *otlptracev1.Status {
	var c otlptracev1.Status_StatusCode
	switch status {
	case codes.Ok:
		c = otlptracev1.Status_STATUS_CODE_OK
	case codes.Error:
		c = otlptracev1.Status_STATUS_CODE_ERROR
	default:
		c = otlptracev1.Status_STATUS_CODE_UNSET
	}
	return &otlptracev1.Status{
		Code:    c,
		Message: message,
	}
}

// KeyValues transforms a slice of attribute KeyValues into OTLP key-values.
func KeyValues(attrs []attribute.KeyValue) []*otlpcommonv1.KeyValue {
	if len(attrs) == 0 {
		return nil
	}

	out := make([]*otlpcommonv1.KeyValue, 0, len(attrs))
	for _, kv := range attrs {
		out = append(out, keyValueToPB(kv))
	}
	return out
}

// linksFromPB transforms span Links to OTLP span linksFromPB.
func linksToPB(links []sdktrace.Link) []*otlptracev1.Span_Link {
	if len(links) == 0 {
		return nil
	}

	sl := make([]*otlptracev1.Span_Link, 0, len(links))
	for _, otLink := range links {
		// This redefinition is necessary to prevent otLink.*ID[:] copies
		// being reused -- in short we need a new otLink per iteration.
		otLink := otLink

		tid := otLink.SpanContext.TraceID()
		sid := otLink.SpanContext.SpanID()

		flags := buildSpanFlags(otLink.SpanContext)

		sl = append(sl, &otlptracev1.Span_Link{
			TraceId:                tid[:],
			SpanId:                 sid[:],
			Attributes:             KeyValues(otLink.Attributes),
			DroppedAttributesCount: uint32(otLink.DroppedAttributeCount),
			Flags:                  flags,
		})
	}
	return sl
}

func buildSpanFlags(sc trace.SpanContext) uint32 {
	flags := otlptracev1.SpanFlags_SPAN_FLAGS_CONTEXT_HAS_IS_REMOTE_MASK
	if sc.IsRemote() {
		flags |= otlptracev1.SpanFlags_SPAN_FLAGS_CONTEXT_IS_REMOTE_MASK
	}

	return uint32(flags)
}

// spanEventsToPB transforms span Events to an OTLP span events.
func spanEventsToPB(es []sdktrace.Event) []*otlptracev1.Span_Event {
	if len(es) == 0 {
		return nil
	}

	events := make([]*otlptracev1.Span_Event, len(es))
	// Transform message events
	for i := 0; i < len(es); i++ {
		events[i] = &otlptracev1.Span_Event{
			Name:                   es[i].Name,
			TimeUnixNano:           uint64(es[i].Time.UnixNano()),
			Attributes:             KeyValues(es[i].Attributes),
			DroppedAttributesCount: uint32(es[i].DroppedAttributeCount),
		}
	}
	return events
}

// spanKindToPB transforms a SpanKind to an OTLP span kind.
func spanKindToPB(kind trace.SpanKind) otlptracev1.Span_SpanKind {
	switch kind {
	case trace.SpanKindInternal:
		return otlptracev1.Span_SPAN_KIND_INTERNAL
	case trace.SpanKindClient:
		return otlptracev1.Span_SPAN_KIND_CLIENT
	case trace.SpanKindServer:
		return otlptracev1.Span_SPAN_KIND_SERVER
	case trace.SpanKindProducer:
		return otlptracev1.Span_SPAN_KIND_PRODUCER
	case trace.SpanKindConsumer:
		return otlptracev1.Span_SPAN_KIND_CONSUMER
	default:
		return otlptracev1.Span_SPAN_KIND_UNSPECIFIED
	}
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
	return spanKindFromPB(s.pb.Kind)
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
	return linksFromPB(s.pb.Links)
}

func (s *readOnlySpan) Events() []sdktrace.Event {
	return spanEventsFromPB(s.pb.Events)
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

// linksFromPB transforms OTLP span links to span Links.
func linksFromPB(links []*otlptracev1.Span_Link) []sdktrace.Link {
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

// spanEventsFromPB transforms OTLP span events to span Events.
func spanEventsFromPB(es []*otlptracev1.Span_Event) []sdktrace.Event {
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

// spanKindFromPB transforms a an OTLP span kind to SpanKind.
func spanKindFromPB(kind otlptracev1.Span_SpanKind) trace.SpanKind {
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
			Value: attrValue(a.Value),
		}
		out = append(out, kv)
	}
	return out
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

func anyArrayToAttrValue(anyVals []*otlpcommonv1.AnyValue) attribute.Value {
	vals := make([]attribute.Value, 0, len(anyVals))
	types := map[attribute.Type]int{}
	for _, v := range anyVals {
		val := attrValue(v)
		types[val.Type()]++
		vals = append(vals, val)
	}

	var arrType attribute.Type
	switch len(types) {
	case 0:
		// empty; assume string slice
		return attribute.StringSliceValue(nil)
	case 1:
		for arrType = range types {
		}
	default:
		slog.Error("anyArrayToAttrValue: mixed types in Any array",
			"types", fmt.Sprintf("%v", types))
		return attribute.StringValue(fmt.Sprintf("%v", vals))
	}

	switch arrType {
	case attribute.STRING:
		return stringArray(anyVals)
	case attribute.INT64:
		return intArray(anyVals)
	case attribute.FLOAT64:
		return doubleArray(anyVals)
	case attribute.BOOL:
		return boolArray(anyVals)
	default:
		slog.Error("anyArrayToAttrValue: unhandled array value type conversion", "type", arrType)
		return attribute.StringValue(fmt.Sprintf("UNHANDLED ARRAY ELEM TYPE: %+v (%s)", vals, arrType))
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

func LogsFromPB(resLogs []*otlplogsv1.ResourceLogs) []sdklog.Record {
	logs := []sdklog.Record{}
	for _, rl := range resLogs {
		for _, scopeLog := range rl.GetScopeLogs() {
			for _, rec := range scopeLog.GetLogRecords() {
				var logRec sdklog.Record
				logRec.SetTraceID(trace.TraceID(rec.GetTraceId()))
				logRec.SetSpanID(trace.SpanID(rec.GetSpanId()))
				logRec.SetTimestamp(time.Unix(0, int64(rec.GetTimeUnixNano())))
				logRec.SetBody(logValueFromPB(rec.GetBody()))
				logRec.SetSeverity(log.Severity(rec.GetSeverityNumber()))
				logRec.SetSeverityText(rec.GetSeverityText())
				logRec.SetObservedTimestamp(time.Unix(0, int64(rec.GetObservedTimeUnixNano())))
				logRec.SetAttributes(logKVs(rec.GetAttributes())...)
				logs = append(logs, logRec)
			}
		}
	}
	return logs
}

func logKVs(kvs []*otlpcommonv1.KeyValue) []log.KeyValue {
	res := make([]log.KeyValue, len(kvs))
	for i, kv := range kvs {
		res[i] = logKeyValue(kv)
	}
	return res
}

func logKeyValue(v *otlpcommonv1.KeyValue) log.KeyValue {
	return log.KeyValue{
		Key:   v.GetKey(),
		Value: logValueFromPB(v.GetValue()),
	}
}

func attrValue(v *otlpcommonv1.AnyValue) attribute.Value {
	switch x := v.Value.(type) {
	case *otlpcommonv1.AnyValue_StringValue:
		return attribute.StringValue(v.GetStringValue())
	case *otlpcommonv1.AnyValue_DoubleValue:
		return attribute.Float64Value(v.GetDoubleValue())
	case *otlpcommonv1.AnyValue_IntValue:
		return attribute.Int64Value(v.GetIntValue())
	case *otlpcommonv1.AnyValue_BoolValue:
		return attribute.BoolValue(v.GetBoolValue())
	case *otlpcommonv1.AnyValue_ArrayValue:
		return anyArrayToAttrValue(x.ArrayValue.GetValues())
	case *otlpcommonv1.AnyValue_BytesValue:
		return attribute.StringValue(string(x.BytesValue))
	default:
		slog.Error("otlpcommonv1.AnyValue -> attribute.Value: unhandled type conversion", "type", fmt.Sprintf("%T", x))
		return attribute.StringValue(fmt.Sprintf("UNHANDLED ATTR TYPE: %v (%T)", x, x))
	}
}

func logValueFromPB(v *otlpcommonv1.AnyValue) log.Value {
	switch x := v.Value.(type) {
	case *otlpcommonv1.AnyValue_StringValue:
		return log.StringValue(v.GetStringValue())
	case *otlpcommonv1.AnyValue_DoubleValue:
		return log.Float64Value(v.GetDoubleValue())
	case *otlpcommonv1.AnyValue_IntValue:
		return log.Int64Value(v.GetIntValue())
	case *otlpcommonv1.AnyValue_BoolValue:
		return log.BoolValue(v.GetBoolValue())
	case *otlpcommonv1.AnyValue_KvlistValue:
		kvs := make([]log.KeyValue, 0, len(x.KvlistValue.GetValues()))
		for _, kv := range x.KvlistValue.GetValues() {
			kvs = append(kvs, logKeyValue(kv))
		}
		return log.MapValue(kvs...)
	case *otlpcommonv1.AnyValue_ArrayValue:
		vals := make([]log.Value, 0, len(x.ArrayValue.GetValues()))
		for _, v := range x.ArrayValue.GetValues() {
			vals = append(vals, logValueFromPB(v))
		}
		return log.SliceValue(vals...)
	case *otlpcommonv1.AnyValue_BytesValue:
		return log.BytesValue(x.BytesValue)
	default:
		slog.Error("unhandled otlpcommonv1.AnyValue -> log.Value conversion", "type", fmt.Sprintf("%T", x))
		return log.StringValue(fmt.Sprintf("UNHANDLED LOG VALUE TYPE: %v (%T)", x, x))
	}
}

// Value transforms an attribute Value into an OTLP AnyValue.
func logValueToPB(v log.Value) *otlpcommonv1.AnyValue {
	av := new(otlpcommonv1.AnyValue)
	switch v.Kind() {
	case log.KindBool:
		av.Value = &otlpcommonv1.AnyValue_BoolValue{
			BoolValue: v.AsBool(),
		}
	case log.KindInt64:
		av.Value = &otlpcommonv1.AnyValue_IntValue{
			IntValue: v.AsInt64(),
		}
	case log.KindFloat64:
		av.Value = &otlpcommonv1.AnyValue_DoubleValue{
			DoubleValue: v.AsFloat64(),
		}
	case log.KindString:
		av.Value = &otlpcommonv1.AnyValue_StringValue{
			StringValue: v.AsString(),
		}
	case log.KindSlice:
		array := &otlpcommonv1.ArrayValue{}
		for _, e := range v.AsSlice() {
			array.Values = append(array.Values, logValueToPB(e))
		}
		av.Value = &otlpcommonv1.AnyValue_ArrayValue{
			ArrayValue: array,
		}
	case log.KindMap:
		kvList := &otlpcommonv1.KeyValueList{}
		for _, e := range v.AsMap() {
			kvList.Values = append(kvList.Values, &otlpcommonv1.KeyValue{
				Key:   e.Key,
				Value: logValueToPB(e.Value),
			})
		}
		av.Value = &otlpcommonv1.AnyValue_KvlistValue{
			KvlistValue: kvList,
		}
	default:
		av.Value = &otlpcommonv1.AnyValue_StringValue{
			StringValue: "INVALID",
		}
	}
	return av
}
