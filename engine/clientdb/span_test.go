package clientdb

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
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
