package clientdb

import (
	"fmt"
	"log/slog"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
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

// UnmarshalProtoList decodes a list MarshalProtoList encoded, cloning base
// for each element.
func UnmarshalProtoList[T proto.Message](pb []byte, base T, out *[]T) error {
	var protos []T
	for len(pb) > 0 {
		msg, n := protowire.ConsumeBytes(pb)
		if n < 0 {
			return fmt.Errorf("failed to decode list element: %w", protowire.ParseError(n))
		}
		pl := proto.Clone(base).(T)
		if err := proto.Unmarshal(msg, pl); err != nil {
			return fmt.Errorf("failed to proto unmarshal into %T: %w", pl, err)
		}
		protos = append(protos, pl)
		pb = pb[n:]
	}
	*out = protos
	return nil
}

// MarshalProtoList encodes protos as a sequence of length-delimited binary
// protobuf messages, the store's form for attribute, event and link lists.
// Binary rather than JSON: encoding and decoding these lists was a large
// share of the engine's CPU in builds with many execs.
func MarshalProtoList[T proto.Message](protos []T) ([]byte, error) {
	buf := make([]byte, 0, 64*len(protos))
	for _, msg := range protos {
		buf = protowire.AppendVarint(buf, uint64(proto.Size(msg)))
		var err error
		buf, err = proto.MarshalOptions{UseCachedSize: true}.MarshalAppend(buf, msg)
		if err != nil {
			return nil, fmt.Errorf("failed to proto marshal: %w", err)
		}
	}
	return buf, nil
}

// Attributes returns the attributes of the span
func (ros *readOnlySpan) Attributes() []attribute.KeyValue {
	var attrs []*otlpcommonv1.KeyValue
	if err := UnmarshalProtoList(ros.DB.Attributes, &otlpcommonv1.KeyValue{}, &attrs); err != nil {
		slog.Warn("failed to unmarshal attributes", "error", err)
	}
	return telemetry.AttributesFromProto(attrs)
}

// Links returns the links of the span
func (ros *readOnlySpan) Links() []sdktrace.Link {
	var links []*otlptracev1.Span_Link
	if err := UnmarshalProtoList(ros.DB.Links, &otlptracev1.Span_Link{}, &links); err != nil {
		slog.Warn("failed to unmarshal links", "error", err)
	}
	return telemetry.SpanLinksFromPB(links)
}

// Events returns the events of the span
func (ros *readOnlySpan) Events() []sdktrace.Event {
	var events []*otlptracev1.Span_Event
	if err := UnmarshalProtoList(ros.DB.Events, &otlptracev1.Span_Event{}, &events); err != nil {
		slog.Warn("failed to unmarshal events", "error", err)
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
	res := &otlpresourcev1.Resource{}
	if err := proto.Unmarshal(ros.DB.Resource, res); err != nil {
		slog.Warn("failed to unmarshal resource", "error", err)
	}
	return resource.NewSchemaless(telemetry.AttributesFromProto(res.Attributes)...)
}

// InstrumentationLibrary returns the instrumentation library.
//
//nolint:staticcheck // sdktrace.ReadOnlySpan still requires this deprecated method.
func (ros *readOnlySpan) InstrumentationLibrary() instrumentation.Library {
	return ros.InstrumentationScope()
}

// InstrumentationScope returns the instrumentation scope
func (ros *readOnlySpan) InstrumentationScope() instrumentation.Scope {
	// The instrumentation scope is stored as binary protobuf.
	is := &otlpcommonv1.InstrumentationScope{}
	if err := proto.Unmarshal(ros.DB.InstrumentationScope, is); err != nil {
		return instrumentation.Scope{}
	}
	return telemetry.InstrumentationScopeFromPB(is)
}

// Ended returns whether the span has ended
func (ros *readOnlySpan) Ended() bool {
	return ros.DB.EndTime.Valid
}

// SpansToPB converts span rows to OTLP, as telemetry.SpansToPB does for their
// ReadOnly views, without decoding each row into SDK values and encoding it
// back. A row's attributes and events were encoded from SDK values, so their
// OTLP form is passed through as stored. Each distinct resource and
// instrumentation scope is decoded once per call.
func SpansToPB(rows []Span) []*otlptracev1.ResourceSpans {
	if len(rows) == 0 {
		return nil
	}

	type decodedResource struct {
		key attribute.Distinct
		pb  *otlpresourcev1.Resource
	}
	resources := map[string]decodedResource{}
	scopes := map[string]instrumentation.Scope{}

	type key struct {
		r  attribute.Distinct
		is instrumentation.Scope
	}
	var rss []*otlptracev1.ResourceSpans
	rsm := make(map[attribute.Distinct]*otlptracev1.ResourceSpans)
	ssm := make(map[key]*otlptracev1.ScopeSpans)
	for i := range rows {
		row := &rows[i]
		res, ok := resources[string(row.Resource)]
		if !ok {
			r := (&readOnlySpan{DB: row}).Resource()
			res = decodedResource{key: r.Equivalent(), pb: telemetry.ResourcePtrToPB(r)}
			resources[string(row.Resource)] = res
		}
		scope, ok := scopes[string(row.InstrumentationScope)]
		if !ok {
			scope = (&readOnlySpan{DB: row}).InstrumentationScope()
			scopes[string(row.InstrumentationScope)] = scope
		}

		k := key{r: res.key, is: scope}
		scopeSpans, sOk := ssm[k]
		if !sOk {
			scopeSpans = &otlptracev1.ScopeSpans{
				Scope:     telemetry.InstrumentationScopeToPB(scope),
				Spans:     []*otlptracev1.Span{},
				SchemaUrl: scope.SchemaURL,
			}
			ssm[k] = scopeSpans
		}
		scopeSpans.Spans = append(scopeSpans.Spans, row.spanPB())

		rs, rOk := rsm[res.key]
		if !rOk {
			rs = &otlptracev1.ResourceSpans{
				Resource:   res.pb,
				ScopeSpans: []*otlptracev1.ScopeSpans{scopeSpans},
			}
			rsm[res.key] = rs
			rss = append(rss, rs)
			continue
		}
		if !sOk {
			rs.ScopeSpans = append(rs.ScopeSpans, scopeSpans)
		}
	}
	return rss
}

// spanPB is the OTLP span telemetry.SpansToPB makes of span.ReadOnly().
func (span *Span) spanPB() *otlptracev1.Span {
	ros := &readOnlySpan{DB: span}
	tid := ros.SpanContext().TraceID()
	sid := ros.SpanContext().SpanID()

	var attrs []*otlpcommonv1.KeyValue
	if err := UnmarshalProtoList(span.Attributes, &otlpcommonv1.KeyValue{}, &attrs); err != nil {
		slog.Warn("failed to unmarshal attributes", "error", err)
	}
	var events []*otlptracev1.Span_Event
	if err := UnmarshalProtoList(span.Events, &otlptracev1.Span_Event{}, &events); err != nil {
		slog.Warn("failed to unmarshal events", "error", err)
	}

	// Links are few; their round trip through SDK values keeps the stream
	// exactly as it was.
	pb := &otlptracev1.Span{
		TraceId:                tid[:],
		SpanId:                 sid[:],
		TraceState:             ros.SpanContext().TraceState().String(),
		Status:                 statusPB(codes.Code(span.StatusCode), span.StatusMessage),
		StartTimeUnixNano:      uint64(ros.StartTime().UnixNano()),
		EndTimeUnixNano:        uint64(ros.EndTime().UnixNano()),
		Links:                  telemetry.SpanLinksToPB(ros.Links()),
		Kind:                   spanKindPB(span.Kind),
		Name:                   span.Name,
		Attributes:             attrs,
		Events:                 events,
		DroppedAttributesCount: uint32(span.DroppedAttributesCount),
		DroppedEventsCount:     uint32(span.DroppedEventsCount),
		DroppedLinksCount:      uint32(span.DroppedLinksCount),
		// A stored parent is never remote.
		Flags: uint32(otlptracev1.SpanFlags_SPAN_FLAGS_CONTEXT_HAS_IS_REMOTE_MASK),
	}
	if psid := ros.Parent().SpanID(); psid.IsValid() {
		pb.ParentSpanId = psid[:]
	}
	return pb
}

func statusPB(code codes.Code, message string) *otlptracev1.Status {
	c := otlptracev1.Status_STATUS_CODE_UNSET
	switch code {
	case codes.Ok:
		c = otlptracev1.Status_STATUS_CODE_OK
	case codes.Error:
		c = otlptracev1.Status_STATUS_CODE_ERROR
	}
	return &otlptracev1.Status{Code: c, Message: message}
}

func spanKindPB(kind string) otlptracev1.Span_SpanKind {
	switch kind {
	case "internal":
		return otlptracev1.Span_SPAN_KIND_INTERNAL
	case "server":
		return otlptracev1.Span_SPAN_KIND_SERVER
	case "client":
		return otlptracev1.Span_SPAN_KIND_CLIENT
	case "producer":
		return otlptracev1.Span_SPAN_KIND_PRODUCER
	case "consumer":
		return otlptracev1.Span_SPAN_KIND_CONSUMER
	default:
		return otlptracev1.Span_SPAN_KIND_UNSPECIFIED
	}
}
