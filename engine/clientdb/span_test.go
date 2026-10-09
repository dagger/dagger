package clientdb

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// legacyMarshalProtoJSONs is the previous encoding: each element through
// json.Marshal as a json.RawMessage, which re-validates and compacts it.
func legacyMarshalProtoJSONs[T proto.Message](protos []T) ([]byte, error) {
	msgs := make([]json.RawMessage, len(protos))
	for i, msg := range protos {
		pl, err := protojson.Marshal(msg)
		if err != nil {
			return nil, err
		}
		msgs[i] = json.RawMessage(pl)
	}
	return json.Marshal(msgs)
}

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

// TestMarshalProtoJSONs pins that dropping the json.Marshal pass changes no
// decoded value: the array decodes to the same messages as the previous
// encoding, through both protojson and plain JSON, and the readers that scan
// the stored bytes for attribute keys still find them.
func TestMarshalProtoJSONs(t *testing.T) {
	t.Run("attributes", func(t *testing.T) {
		attrs := sampleSpanAttrs()
		got, err := MarshalProtoJSONs(attrs)
		require.NoError(t, err)
		legacy, err := legacyMarshalProtoJSONs(attrs)
		require.NoError(t, err)

		var gotPB, legacyPB []*otlpcommonv1.KeyValue
		require.NoError(t, UnmarshalProtoJSONs(got, &otlpcommonv1.KeyValue{}, &gotPB))
		require.NoError(t, UnmarshalProtoJSONs(legacy, &otlpcommonv1.KeyValue{}, &legacyPB))
		require.Len(t, gotPB, len(attrs))
		for i := range attrs {
			require.True(t, proto.Equal(attrs[i], gotPB[i]), "attr %d: %v != %v", i, attrs[i], gotPB[i])
			require.True(t, proto.Equal(legacyPB[i], gotPB[i]), "attr %d", i)
		}

		var gotAny, legacyAny any
		require.NoError(t, json.Unmarshal(got, &gotAny))
		require.NoError(t, json.Unmarshal(legacy, &legacyAny))
		require.Equal(t, legacyAny, gotAny)

		for _, kv := range attrs {
			require.True(t, bytes.Contains(got, []byte(`"`+kv.Key+`"`)), "key %q", kv.Key)
		}
		dig, ok := protoJSONStringAttr(got, dagDigestAttrMarker)
		require.True(t, ok)
		require.Equal(t, "xxh3:0123456789abcdef", dig)
	})

	t.Run("events and links", func(t *testing.T) {
		events := []*otlptracev1.Span_Event{{Name: "e", TimeUnixNano: 7, Attributes: sampleSpanAttrs()}}
		links := []*otlptracev1.Span_Link{{TraceId: bytes.Repeat([]byte{1}, 16), SpanId: bytes.Repeat([]byte{2}, 8), Attributes: sampleSpanAttrs()}}

		gotEvents, err := MarshalProtoJSONs(events)
		require.NoError(t, err)
		var eventsPB []*otlptracev1.Span_Event
		require.NoError(t, UnmarshalProtoJSONs(gotEvents, &otlptracev1.Span_Event{}, &eventsPB))
		require.Len(t, eventsPB, 1)
		require.True(t, proto.Equal(events[0], eventsPB[0]))

		gotLinks, err := MarshalProtoJSONs(links)
		require.NoError(t, err)
		var linksPB []*otlptracev1.Span_Link
		require.NoError(t, UnmarshalProtoJSONs(gotLinks, &otlptracev1.Span_Link{}, &linksPB))
		require.Len(t, linksPB, 1)
		require.True(t, proto.Equal(links[0], linksPB[0]))
	})

	t.Run("empty", func(t *testing.T) {
		// causalLinkTargets treats rows of at most len("[]") as linkless.
		for _, protos := range [][]*otlpcommonv1.KeyValue{nil, {}} {
			got, err := MarshalProtoJSONs(protos)
			require.NoError(t, err)
			require.Equal(t, "[]", string(got))
		}
	})

	t.Run("nil element", func(t *testing.T) {
		got, err := MarshalProtoJSONs([]*otlpcommonv1.KeyValue{nil, strAttr("k", "v")})
		require.NoError(t, err)
		legacy, err := legacyMarshalProtoJSONs([]*otlpcommonv1.KeyValue{nil, strAttr("k", "v")})
		require.NoError(t, err)
		var gotAny, legacyAny any
		require.NoError(t, json.Unmarshal(got, &gotAny))
		require.NoError(t, json.Unmarshal(legacy, &legacyAny))
		require.Equal(t, legacyAny, gotAny)
	})

	t.Run("not valid", func(t *testing.T) {
		// Invalid UTF-8 must still fail the encode, as before.
		_, err := MarshalProtoJSONs([]*otlpcommonv1.KeyValue{strAttr("k", strings.Repeat("\xff", 2))})
		require.Error(t, err)
	})
}

func BenchmarkMarshalProtoJSONs(b *testing.B) {
	attrs := sampleSpanAttrs()
	b.Run("current", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := MarshalProtoJSONs(attrs); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("legacy", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := legacyMarshalProtoJSONs(attrs); err != nil {
				b.Fatal(err)
			}
		}
	})
}
