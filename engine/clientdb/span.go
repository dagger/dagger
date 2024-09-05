package clientdb

import (
	"encoding/json"
	"fmt"
	"log/slog"
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
	"google.golang.org/protobuf/encoding/protojson"
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

func UnmarshalProtoJSONs[T proto.Message](pb []byte, base T, out *[]T) error {
	var msgs []json.RawMessage
	if err := json.Unmarshal(pb, &msgs); err != nil {
		return fmt.Errorf("failed to json unmarshal: %w", err)
	}
	protos := make([]T, len(msgs))
	for i, msg := range msgs {
		pl := proto.Clone(base).(T)
		if err := protojson.Unmarshal(msg, pl); err != nil {
			return fmt.Errorf("failed to protojson unmarshal %s into %T: %w", msg, pl, err)
		}
		protos[i] = pl
	}
	*out = protos
	return nil
}

func MarshalProtoJSONs[T proto.Message](protos []T) ([]byte, error) {
	msgs := make([]json.RawMessage, len(protos))
	for i, msg := range protos {
		pl, err := protojson.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to protojson marshal: %w", err)
		}
		msgs[i] = json.RawMessage(pl)
	}
	return json.Marshal(msgs)
}

// Attributes returns the attributes of the span
func (ros *readOnlySpan) Attributes() []attribute.KeyValue {
	var attrs []*otlpcommonv1.KeyValue
	if err := UnmarshalProtoJSONs(ros.DB.Attributes, &otlpcommonv1.KeyValue{}, &attrs); err != nil {
		slog.Warn("failed to unmarshal attributes", "error", err)
	}
	return telemetry.AttributesFromProto(attrs)
}

// Links returns the links of the span
func (ros *readOnlySpan) Links() []sdktrace.Link {
	var links []*otlptracev1.Span_Link
	if err := UnmarshalProtoJSONs(ros.DB.Links, &otlptracev1.Span_Link{}, &links); err != nil {
		slog.Warn("failed to unmarshal links", "error", err)
	}
	return telemetry.SpanLinksFromPB(links)
}

// Events returns the events of the span
func (ros *readOnlySpan) Events() []sdktrace.Event {
	var events []*otlptracev1.Span_Event
	if err := UnmarshalProtoJSONs(ros.DB.Events, &otlptracev1.Span_Event{}, &events); err != nil {
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
	if err := protojson.Unmarshal(ros.DB.Resource, res); err != nil {
		slog.Warn("failed to unmarshal resource", "error", err)
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
	is := &otlpcommonv1.InstrumentationScope{}
	if err := protojson.Unmarshal(ros.DB.InstrumentationScope, is); err != nil {
		return instrumentation.Scope{}
	}
	return telemetry.InstrumentationScopeFromPB(is)
}

// Ended returns whether the span has ended
func (ros *readOnlySpan) Ended() bool {
	return ros.DB.EndTime.Valid
}
