package clientdb

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"
	"unicode/utf8"

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

// MarshalKeyValueList encodes attrs as MarshalProtoList encodes
// telemetry.KeyValues(attrs), byte for byte, straight from the SDK values,
// leaving out the attributes whose key is skip when it isn't empty. Like
// proto.Marshal, it fails on a string that isn't valid UTF-8.
func MarshalKeyValueList(attrs []attribute.KeyValue, skip attribute.Key) ([]byte, error) {
	b := make([]byte, 0, 64*len(attrs))
	for _, kv := range attrs {
		if skip != "" && kv.Key == skip {
			continue
		}
		var err error
		if b, err = appendKeyValue(b, kv); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// MarshalEventList encodes events as MarshalProtoList encodes
// telemetry.SpanEventsToPB(events), byte for byte, straight from the SDK
// values. Like proto.Marshal, it fails on a string that isn't valid UTF-8.
func MarshalEventList(events []sdktrace.Event) ([]byte, error) {
	b := make([]byte, 0, 64*len(events))
	var ev []byte
	for _, e := range events {
		if !utf8.ValidString(e.Name) {
			return nil, errInvalidUTF8
		}
		// Span_Event's fields, in field order.
		ev = ev[:0]
		if t := uint64(e.Time.UnixNano()); t != 0 {
			ev = protowire.AppendTag(ev, 1, protowire.Fixed64Type)
			ev = protowire.AppendFixed64(ev, t)
		}
		if e.Name != "" {
			ev = protowire.AppendTag(ev, 2, protowire.BytesType)
			ev = protowire.AppendString(ev, e.Name)
		}
		for _, kv := range e.Attributes {
			ev = protowire.AppendTag(ev, 3, protowire.BytesType)
			var err error
			if ev, err = appendKeyValue(ev, kv); err != nil {
				return nil, err
			}
		}
		if n := uint32(e.DroppedAttributeCount); n != 0 {
			ev = protowire.AppendTag(ev, 4, protowire.VarintType)
			ev = protowire.AppendVarint(ev, uint64(n))
		}
		b = protowire.AppendBytes(b, ev)
	}
	return b, nil
}

var errInvalidUTF8 = errors.New("failed to proto marshal: string field contains invalid UTF-8")

// appendKeyValue appends kv's length and encoding as the OTLP KeyValue
// otel-go makes of it: AnyValue's oneof carries the value, even a zero one,
// slices as an ArrayValue, and any other type as the string "INVALID".
func appendKeyValue(b []byte, kv attribute.KeyValue) ([]byte, error) {
	if !utf8.ValidString(string(kv.Key)) {
		return nil, errInvalidUTF8
	}
	v := kv.Value
	var (
		bools  []bool
		ints   []int64
		floats []float64
		strs   []string
		str    string
	)
	// The AnyValue's size, and its ArrayValue's for slices.
	var size, arr int
	isSlice := false
	switch v.Type() {
	case attribute.BOOL:
		size = 2
	case attribute.INT64:
		size = 1 + protowire.SizeVarint(uint64(v.AsInt64()))
	case attribute.FLOAT64:
		size = 1 + 8
	case attribute.STRING:
		str = v.AsString()
		if !utf8.ValidString(str) {
			return nil, errInvalidUTF8
		}
		size = 1 + protowire.SizeBytes(len(str))
	case attribute.BOOLSLICE:
		isSlice = true
		bools = v.AsBoolSlice()
		arr = len(bools) * (1 + protowire.SizeBytes(2))
	case attribute.INT64SLICE:
		isSlice = true
		ints = v.AsInt64Slice()
		for _, i := range ints {
			arr += 1 + protowire.SizeBytes(1+protowire.SizeVarint(uint64(i)))
		}
	case attribute.FLOAT64SLICE:
		isSlice = true
		floats = v.AsFloat64Slice()
		arr = len(floats) * (1 + protowire.SizeBytes(1+8))
	case attribute.STRINGSLICE:
		isSlice = true
		strs = v.AsStringSlice()
		for _, s := range strs {
			if !utf8.ValidString(s) {
				return nil, errInvalidUTF8
			}
			arr += 1 + protowire.SizeBytes(1+protowire.SizeBytes(len(s)))
		}
	default:
		str = "INVALID"
		size = 1 + protowire.SizeBytes(len(str))
	}
	if isSlice {
		size = 1 + protowire.SizeBytes(arr)
	}

	n := 1 + protowire.SizeBytes(size)
	if kv.Key != "" {
		n += 1 + protowire.SizeBytes(len(kv.Key))
	}
	b = protowire.AppendVarint(b, uint64(n))
	if kv.Key != "" {
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendString(b, string(kv.Key))
	}
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendVarint(b, uint64(size))
	if isSlice {
		b = protowire.AppendTag(b, 5, protowire.BytesType)
		b = protowire.AppendVarint(b, uint64(arr))
	}
	switch v.Type() {
	case attribute.BOOL:
		b = appendBoolValue(b, v.AsBool())
	case attribute.INT64:
		b = appendIntValue(b, v.AsInt64())
	case attribute.FLOAT64:
		b = appendDoubleValue(b, v.AsFloat64())
	case attribute.BOOLSLICE:
		for _, x := range bools {
			b = protowire.AppendTag(b, 1, protowire.BytesType)
			b = protowire.AppendVarint(b, 2)
			b = appendBoolValue(b, x)
		}
	case attribute.INT64SLICE:
		for _, x := range ints {
			b = protowire.AppendTag(b, 1, protowire.BytesType)
			b = protowire.AppendVarint(b, uint64(1+protowire.SizeVarint(uint64(x))))
			b = appendIntValue(b, x)
		}
	case attribute.FLOAT64SLICE:
		for _, x := range floats {
			b = protowire.AppendTag(b, 1, protowire.BytesType)
			b = protowire.AppendVarint(b, 1+8)
			b = appendDoubleValue(b, x)
		}
	case attribute.STRINGSLICE:
		for _, x := range strs {
			b = protowire.AppendTag(b, 1, protowire.BytesType)
			b = protowire.AppendVarint(b, uint64(1+protowire.SizeBytes(len(x))))
			b = appendStringValue(b, x)
		}
	default:
		b = appendStringValue(b, str)
	}
	return b, nil
}

// AnyValue's oneof fields.
func appendStringValue(b []byte, s string) []byte {
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	return protowire.AppendString(b, s)
}

func appendBoolValue(b []byte, v bool) []byte {
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	return protowire.AppendVarint(b, protowire.EncodeBool(v))
}

func appendIntValue(b []byte, v int64) []byte {
	b = protowire.AppendTag(b, 3, protowire.VarintType)
	return protowire.AppendVarint(b, uint64(v))
}

func appendDoubleValue(b []byte, v float64) []byte {
	b = protowire.AppendTag(b, 4, protowire.Fixed64Type)
	return protowire.AppendFixed64(b, math.Float64bits(v))
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

// AppendTraceRequestPB appends the binary encoding of the OTLP trace export
// request carrying rows: byte for byte what proto.Marshal makes of
// ExportTraceServiceRequest{ResourceSpans: SpansToPB(rows)}, without building
// its messages. A row's stored attribute and event lists are already their
// elements' encodings, each with its length in front, so each element is
// written after its field's tag. Resources, scopes and links are encoded as
// SpansToPB makes them, each distinct resource and scope once per call.
func AppendTraceRequestPB(b []byte, rows []Span) ([]byte, error) {
	type scopeGroup struct {
		scope     []byte // the InstrumentationScope's encoding, nil for none
		schemaURL string
		spans     []byte // each span as ScopeSpans' field 2: tag, length and encoding
	}
	type resourceGroup struct {
		resource []byte // the Resource's encoding
		scopes   []*scopeGroup
		byScope  map[instrumentation.Scope]*scopeGroup
	}
	type decodedResource struct {
		key attribute.Distinct
		pb  []byte
	}
	resources := map[string]decodedResource{}
	scopes := map[string]instrumentation.Scope{}
	var groups []*resourceGroup
	byResource := map[attribute.Distinct]*resourceGroup{}
	var span []byte
	for i := range rows {
		row := &rows[i]
		res, ok := resources[string(row.Resource)]
		if !ok {
			r := (&readOnlySpan{DB: row}).Resource()
			pb, err := proto.Marshal(telemetry.ResourcePtrToPB(r))
			if err != nil {
				return nil, fmt.Errorf("failed to proto marshal resource: %w", err)
			}
			res = decodedResource{key: r.Equivalent(), pb: pb}
			resources[string(row.Resource)] = res
		}
		scope, ok := scopes[string(row.InstrumentationScope)]
		if !ok {
			scope = (&readOnlySpan{DB: row}).InstrumentationScope()
			scopes[string(row.InstrumentationScope)] = scope
		}

		rg := byResource[res.key]
		if rg == nil {
			rg = &resourceGroup{resource: res.pb, byScope: map[instrumentation.Scope]*scopeGroup{}}
			byResource[res.key] = rg
			groups = append(groups, rg)
		}
		sg := rg.byScope[scope]
		if sg == nil {
			sg = &scopeGroup{schemaURL: scope.SchemaURL}
			if pb := telemetry.InstrumentationScopeToPB(scope); pb != nil {
				enc, err := proto.Marshal(pb)
				if err != nil {
					return nil, fmt.Errorf("failed to proto marshal instrumentation scope: %w", err)
				}
				sg.scope = enc
			}
			rg.byScope[scope] = sg
			rg.scopes = append(rg.scopes, sg)
		}
		var err error
		span, err = row.appendSpanPB(span[:0])
		if err != nil {
			return nil, err
		}
		sg.spans = protowire.AppendTag(sg.spans, 2, protowire.BytesType)
		sg.spans = protowire.AppendBytes(sg.spans, span)
	}

	scopeLen := func(sg *scopeGroup) int {
		n := len(sg.spans)
		if sg.scope != nil {
			n += protowire.SizeTag(1) + protowire.SizeBytes(len(sg.scope))
		}
		if sg.schemaURL != "" {
			n += protowire.SizeTag(3) + protowire.SizeBytes(len(sg.schemaURL))
		}
		return n
	}
	for _, rg := range groups {
		n := protowire.SizeTag(1) + protowire.SizeBytes(len(rg.resource))
		for _, sg := range rg.scopes {
			n += protowire.SizeTag(2) + protowire.SizeBytes(scopeLen(sg))
		}
		// ExportTraceServiceRequest's field 1: one ResourceSpans.
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendVarint(b, uint64(n))
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, rg.resource)
		for _, sg := range rg.scopes {
			b = protowire.AppendTag(b, 2, protowire.BytesType)
			b = protowire.AppendVarint(b, uint64(scopeLen(sg)))
			if sg.scope != nil {
				b = protowire.AppendTag(b, 1, protowire.BytesType)
				b = protowire.AppendBytes(b, sg.scope)
			}
			b = append(b, sg.spans...)
			if sg.schemaURL != "" {
				b = protowire.AppendTag(b, 3, protowire.BytesType)
				b = protowire.AppendString(b, sg.schemaURL)
			}
		}
	}
	return b, nil
}

// appendSpanPB appends the encoding of the OTLP span spanPB makes of span, in
// field order, as proto.Marshal does. Like proto.Marshal, it fails on a name
// or status message that isn't valid UTF-8; the stored attributes and events
// were checked when they were encoded.
func (span *Span) appendSpanPB(b []byte) ([]byte, error) {
	if !utf8.ValidString(span.Name) || !utf8.ValidString(span.StatusMessage) {
		return nil, errInvalidUTF8
	}
	ros := &readOnlySpan{DB: span}
	sc := ros.SpanContext()
	tid, sid := sc.TraceID(), sc.SpanID()
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendBytes(b, tid[:])
	b = protowire.AppendTag(b, 2, protowire.BytesType)
	b = protowire.AppendBytes(b, sid[:])
	if ts := sc.TraceState().String(); ts != "" {
		b = protowire.AppendTag(b, 3, protowire.BytesType)
		b = protowire.AppendString(b, ts)
	}
	if psid := ros.Parent().SpanID(); psid.IsValid() {
		b = protowire.AppendTag(b, 4, protowire.BytesType)
		b = protowire.AppendBytes(b, psid[:])
	}
	if span.Name != "" {
		b = protowire.AppendTag(b, 5, protowire.BytesType)
		b = protowire.AppendString(b, span.Name)
	}
	if kind := spanKindPB(span.Kind); kind != 0 {
		b = protowire.AppendTag(b, 6, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(kind))
	}
	if start := uint64(ros.StartTime().UnixNano()); start != 0 {
		b = protowire.AppendTag(b, 7, protowire.Fixed64Type)
		b = protowire.AppendFixed64(b, start)
	}
	if end := uint64(ros.EndTime().UnixNano()); end != 0 {
		b = protowire.AppendTag(b, 8, protowire.Fixed64Type)
		b = protowire.AppendFixed64(b, end)
	}
	b = appendStoredList(b, 9, span.Attributes, "attributes")
	b = appendUint32Field(b, 10, uint32(span.DroppedAttributesCount))
	b = appendStoredList(b, 11, span.Events, "events")
	b = appendUint32Field(b, 12, uint32(span.DroppedEventsCount))
	if len(span.Links) > 0 {
		for _, link := range telemetry.SpanLinksToPB(ros.Links()) {
			enc, err := proto.Marshal(link)
			if err != nil {
				return nil, fmt.Errorf("failed to proto marshal link: %w", err)
			}
			b = protowire.AppendTag(b, 13, protowire.BytesType)
			b = protowire.AppendBytes(b, enc)
		}
	}
	b = appendUint32Field(b, 14, uint32(span.DroppedLinksCount))
	status := statusPB(codes.Code(span.StatusCode), span.StatusMessage)
	n := 0
	if status.Message != "" {
		n += protowire.SizeTag(2) + protowire.SizeBytes(len(status.Message))
	}
	if status.Code != 0 {
		n += protowire.SizeTag(3) + protowire.SizeVarint(uint64(status.Code))
	}
	b = protowire.AppendTag(b, 15, protowire.BytesType)
	b = protowire.AppendVarint(b, uint64(n))
	if status.Message != "" {
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendString(b, status.Message)
	}
	if status.Code != 0 {
		b = protowire.AppendTag(b, 3, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(status.Code))
	}
	// A stored parent is never remote.
	b = protowire.AppendTag(b, 16, protowire.Fixed32Type)
	b = protowire.AppendFixed32(b, uint32(otlptracev1.SpanFlags_SPAN_FLAGS_CONTEXT_HAS_IS_REMOTE_MASK))
	return b, nil
}

// appendStoredList appends a list MarshalProtoList encoded as field num's
// repeated elements, each after its tag. Like spanPB, it leaves out the
// whole list when the elements can't be split.
func appendStoredList(b []byte, num protowire.Number, list []byte, what string) []byte {
	start := len(b)
	for len(list) > 0 {
		_, n := protowire.ConsumeBytes(list)
		if n < 0 {
			slog.Warn("failed to unmarshal "+what, "error", fmt.Errorf("failed to decode list element: %w", protowire.ParseError(n)))
			return b[:start]
		}
		b = protowire.AppendTag(b, num, protowire.BytesType)
		b = append(b, list[:n]...)
		list = list[n:]
	}
	return b
}

func appendUint32Field(b []byte, num protowire.Number, v uint32) []byte {
	if v == 0 {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.VarintType)
	return protowire.AppendVarint(b, uint64(v))
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
