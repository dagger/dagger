package clientdb

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func strAttr(k, v string) *otlpcommonv1.KeyValue {
	return &otlpcommonv1.KeyValue{Key: k, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: v}}}
}

func sampleSpanAttrs() []*otlpcommonv1.KeyValue {
	return []*otlpcommonv1.KeyValue{
		strAttr(telemetry.DagDigestAttr, "xxh3:0123456789abcdef"),
		strAttr(telemetry.DagCallAttr, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xfb, 0x01, 0x7f}, 700))),
		strAttr("html", `<a href="x">&amp;</a>`),
		strAttr("unicode", "naïve ☃   \x00 \"quoted\" \\back"),
		{Key: "int", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_IntValue{IntValue: -1 << 62}}},
		{Key: "double", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_DoubleValue{DoubleValue: 0.1}}},
		{Key: "bool", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BoolValue{BoolValue: true}}},
		{Key: "bytes", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: []byte("<&>")}}},
		{Key: "list", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_ArrayValue{ArrayValue: &otlpcommonv1.ArrayValue{
			Values: []*otlpcommonv1.AnyValue{
				{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "a"}},
				{Value: &otlpcommonv1.AnyValue_KvlistValue{KvlistValue: &otlpcommonv1.KeyValueList{Values: []*otlpcommonv1.KeyValue{strAttr("nested", "b")}}}},
			},
		}}}},
		{Key: "empty"},
	}
}

// TestMarshalProtoList pins the store's list encoding: every message
// round-trips, and the readers that scan the stored bytes for attribute keys
// still find them.
func TestMarshalProtoList(t *testing.T) {
	t.Run("attributes", func(t *testing.T) {
		attrs := sampleSpanAttrs()
		got, err := MarshalProtoList(attrs)
		require.NoError(t, err)

		var gotPB []*otlpcommonv1.KeyValue
		require.NoError(t, UnmarshalProtoList(got, &otlpcommonv1.KeyValue{}, &gotPB))
		require.Len(t, gotPB, len(attrs))
		for i := range attrs {
			require.True(t, proto.Equal(attrs[i], gotPB[i]), "attr %d: %v != %v", i, attrs[i], gotPB[i])
		}

		for _, kv := range attrs {
			require.True(t, bytes.Contains(got, []byte(kv.Key)), "key %q", kv.Key)
		}
		dig, ok := listStringAttr(got, telemetry.DagDigestAttr)
		require.True(t, ok)
		require.Equal(t, "xxh3:0123456789abcdef", dig)
	})

	t.Run("events and links", func(t *testing.T) {
		events := []*otlptracev1.Span_Event{{Name: "e", TimeUnixNano: 7, Attributes: sampleSpanAttrs()}}
		links := []*otlptracev1.Span_Link{{TraceId: bytes.Repeat([]byte{1}, 16), SpanId: bytes.Repeat([]byte{2}, 8), Attributes: sampleSpanAttrs()}}

		gotEvents, err := MarshalProtoList(events)
		require.NoError(t, err)
		var eventsPB []*otlptracev1.Span_Event
		require.NoError(t, UnmarshalProtoList(gotEvents, &otlptracev1.Span_Event{}, &eventsPB))
		require.Len(t, eventsPB, 1)
		require.True(t, proto.Equal(events[0], eventsPB[0]))

		gotLinks, err := MarshalProtoList(links)
		require.NoError(t, err)
		var linksPB []*otlptracev1.Span_Link
		require.NoError(t, UnmarshalProtoList(gotLinks, &otlptracev1.Span_Link{}, &linksPB))
		require.Len(t, linksPB, 1)
		require.True(t, proto.Equal(links[0], linksPB[0]))
	})

	t.Run("empty", func(t *testing.T) {
		// causalLinkTargets treats an empty row as linkless.
		for _, protos := range [][]*otlpcommonv1.KeyValue{nil, {}} {
			got, err := MarshalProtoList(protos)
			require.NoError(t, err)
			require.Empty(t, got)
			var decoded []*otlpcommonv1.KeyValue
			require.NoError(t, UnmarshalProtoList(got, &otlpcommonv1.KeyValue{}, &decoded))
			require.Empty(t, decoded)
		}
	})

	t.Run("nil element", func(t *testing.T) {
		// A nil element is kept as an empty message, as protojson kept it as {}.
		got, err := MarshalProtoList([]*otlpcommonv1.KeyValue{nil, strAttr("k", "v")})
		require.NoError(t, err)
		var decoded []*otlpcommonv1.KeyValue
		require.NoError(t, UnmarshalProtoList(got, &otlpcommonv1.KeyValue{}, &decoded))
		require.Len(t, decoded, 2)
		require.True(t, proto.Equal(&otlpcommonv1.KeyValue{}, decoded[0]))
		require.True(t, proto.Equal(strAttr("k", "v"), decoded[1]))
	})

	t.Run("not valid", func(t *testing.T) {
		// Invalid UTF-8 must still fail the encode, as before.
		_, err := MarshalProtoList([]*otlpcommonv1.KeyValue{strAttr("k", strings.Repeat("\xff", 2))})
		require.Error(t, err)
	})

	t.Run("truncated", func(t *testing.T) {
		got, err := MarshalProtoList(sampleSpanAttrs())
		require.NoError(t, err)
		var decoded []*otlpcommonv1.KeyValue
		require.Error(t, UnmarshalProtoList(got[:len(got)-1], &otlpcommonv1.KeyValue{}, &decoded))
	})
}

func BenchmarkMarshalProtoList(b *testing.B) {
	attrs := sampleSpanAttrs()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := MarshalProtoList(attrs); err != nil {
			b.Fatal(err)
		}
	}
}

// TestAppendTraceRequestPB checks that the trace stream's encoding written
// from stored rows is byte for byte what proto.Marshal makes of the request
// SpansToPB builds from the same rows.
func TestAppendTraceRequestPB(t *testing.T) {
	list := func(msgs ...proto.Message) []byte {
		t.Helper()
		b, err := MarshalProtoList(msgs)
		require.NoError(t, err)
		return b
	}
	enc := func(m proto.Message) []byte {
		t.Helper()
		b, err := proto.Marshal(m)
		require.NoError(t, err)
		return b
	}
	attrs := make([]proto.Message, 0, len(sampleSpanAttrs()))
	for _, kv := range sampleSpanAttrs() {
		attrs = append(attrs, kv)
	}
	resA := enc(&otlpresourcev1.Resource{Attributes: []*otlpcommonv1.KeyValue{strAttr("service.name", "dagger-engine"), strAttr("a", "1")}})
	resB := enc(&otlpresourcev1.Resource{Attributes: []*otlpcommonv1.KeyValue{strAttr("service.name", "dagger-cli")}})
	scopeX := enc(&otlpcommonv1.InstrumentationScope{Name: "dagger.io/engine", Version: "v1"})
	events := list(&otlptracev1.Span_Event{
		Name:                   "evt",
		TimeUnixNano:           1_700_000_000_000_000_123,
		Attributes:             []*otlpcommonv1.KeyValue{strAttr("k", "v")},
		DroppedAttributesCount: 2,
	}, &otlptracev1.Span_Event{Name: "bare"})
	links := list(&otlptracev1.Span_Link{
		TraceId:    bytes.Repeat([]byte{0xab}, 16),
		SpanId:     bytes.Repeat([]byte{0xcd}, 8),
		TraceState: "x=y",
		Attributes: []*otlpcommonv1.KeyValue{strAttr(telemetry.LinkPurposeAttr, telemetry.LinkPurposeCause)},
		Flags:      0x301,
	})
	rows := []Span{
		{
			TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "1112131415161718", TraceState: "a=b",
			ParentSpanID: sql.NullString{String: "2122232425262728", Valid: true},
			Flags:        1, Name: "full", Kind: "server",
			StartTime: 1_700_000_000_000_000_000, EndTime: sql.NullInt64{Int64: 1_700_000_001_000_000_000, Valid: true},
			Attributes: list(attrs...), DroppedAttributesCount: 3,
			Events: events, DroppedEventsCount: 4,
			Links: links, DroppedLinksCount: 5,
			Resource: resA, InstrumentationScope: scopeX,
			StatusCode: int64(codes.Error), StatusMessage: "boom",
		},
		// Unfinished, with no parent, kind, attributes or scope.
		{TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "3132333435363738", Name: "open", StartTime: 1, Resource: resA},
		// Attributes whose elements can't be split, under another resource.
		{TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "4142434445464748", Name: "bad", Kind: "client", Attributes: []byte{0xff}, Resource: resB, InstrumentationScope: scopeX, StatusCode: int64(codes.Ok)},
		// The first resource and scope again, after the others.
		{TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "5152535455565758", Name: "again", Kind: "internal", Attributes: list(strAttr("k", "v")), Resource: resA, InstrumentationScope: scopeX},
	}
	want, err := proto.Marshal(&coltracepb.ExportTraceServiceRequest{ResourceSpans: SpansToPB(rows)})
	require.NoError(t, err)
	got, err := AppendTraceRequestPB(nil, rows)
	require.NoError(t, err)
	require.Equal(t, want, got)

	got, err = AppendTraceRequestPB(nil, nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

func sdkAttrs() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("s", "naïve ☃ \x00"),
		attribute.String("empty", ""),
		attribute.String("", "no key"),
		attribute.Bool("t", true),
		attribute.Bool("f", false),
		attribute.Int64("i", -1<<62),
		attribute.Int64("zero", 0),
		attribute.Float64("d", 0.1),
		attribute.Float64("nan", math.NaN()),
		attribute.BoolSlice("bs", []bool{true, false}),
		attribute.Int64Slice("is", []int64{0, 1, -1, 1 << 40}),
		attribute.Float64Slice("ds", []float64{0, -2.5}),
		attribute.StringSlice("ss", []string{"", "a", strings.Repeat("b", 300)}),
		attribute.StringSlice("none", nil),
		{Key: "invalid"},
	}
}

// TestMarshalKeyValueList checks that attributes are stored byte for byte as
// MarshalProtoList stores otel-go's messages for them.
func TestMarshalKeyValueList(t *testing.T) {
	attrs := sdkAttrs()
	want, err := MarshalProtoList(telemetry.KeyValues(attrs))
	require.NoError(t, err)
	got, err := MarshalKeyValueList(attrs, "")
	require.NoError(t, err)
	require.Equal(t, want, got)

	want, err = MarshalProtoList(telemetry.KeyValues(append(slices.Clone(attrs[:3]), attrs[4:]...)))
	require.NoError(t, err)
	got, err = MarshalKeyValueList(attrs, "t")
	require.NoError(t, err)
	require.Equal(t, want, got, "the skipped key is left out")

	got, err = MarshalKeyValueList(nil, "")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)

	for _, bad := range []attribute.KeyValue{
		attribute.String("k", "\xff"),
		attribute.String("\xff", "v"),
		attribute.StringSlice("k", []string{"ok", "\xff"}),
	} {
		_, protoErr := MarshalProtoList(telemetry.KeyValues([]attribute.KeyValue{bad}))
		require.Error(t, protoErr)
		_, err := MarshalKeyValueList([]attribute.KeyValue{bad}, "")
		require.Error(t, err, "%v", bad)
	}
}

// TestMarshalEventList checks that events are stored byte for byte as
// MarshalProtoList stores otel-go's messages for them.
func TestMarshalEventList(t *testing.T) {
	events := []sdktrace.Event{
		{Name: "full", Time: time.Unix(1_700_000_000, 123), Attributes: sdkAttrs(), DroppedAttributeCount: 3},
		{Name: "bare", Time: time.Unix(0, 5)},
		{},
	}
	want, err := MarshalProtoList(telemetry.SpanEventsToPB(events))
	require.NoError(t, err)
	got, err := MarshalEventList(events)
	require.NoError(t, err)
	require.Equal(t, want, got)

	got, err = MarshalEventList(nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)

	_, err = MarshalEventList([]sdktrace.Event{{Name: "\xff"}})
	require.Error(t, err)
	_, err = MarshalEventList([]sdktrace.Event{{Name: "ok", Attributes: []attribute.KeyValue{attribute.String("k", "\xff")}}})
	require.Error(t, err)
}
