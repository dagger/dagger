package clientdb

import (
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

func stringAttrsList(t *testing.T, kvs ...string) []byte {
	t.Helper()
	require.Zero(t, len(kvs)%2)
	attrs := make([]*otlpcommonv1.KeyValue, 0, len(kvs)/2)
	for i := 0; i < len(kvs); i += 2 {
		attrs = append(attrs, &otlpcommonv1.KeyValue{
			Key:   kvs[i],
			Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: kvs[i+1]}},
		})
	}
	encoded, err := MarshalProtoList(attrs)
	require.NoError(t, err)
	return encoded
}

// callPayloadLog builds the log row the engine appends for one frame of a
// call's closure: a bytes body holding the encoded call, reserved by its
// content type.
func callPayloadLog(t *testing.T, frame *callpbv1.Call) Log {
	t.Helper()
	payload, err := proto.Marshal(frame)
	require.NoError(t, err)
	body, err := proto.Marshal(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: payload}})
	require.NoError(t, err)
	return Log{
		SpanID:     validString("span"),
		Body:       body,
		Attributes: stringAttrsList(t, telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType),
	}
}

func TestCallIndex(t *testing.T) {
	ctx := t.Context()
	store, err := openStore(ctx, t.TempDir(), "client", 256)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.closeStreams()) }()

	// Spans: one carries its frame (digest + call), one has a digest but no
	// frame (unusable for a rebuild), one is unrelated. A later snapshot of
	// the framed span must win.
	spans := []Span{
		{TraceID: "t", SpanID: "framed", Attributes: stringAttrsList(t,
			telemetry.DagDigestAttr, "xxh3:aaaa", telemetry.DagCallAttr, "AAAA")},
		{TraceID: "t", SpanID: "bare", Attributes: stringAttrsList(t,
			telemetry.DagDigestAttr, "xxh3:bbbb")},
		{TraceID: "t", SpanID: "plain", Attributes: stringAttrsList(t, "k", "v")},
		{TraceID: "t", SpanID: "framed", Attributes: stringAttrsList(t,
			telemetry.DagDigestAttr, "xxh3:aaaa", telemetry.DagCallAttr, "AAAA", "later", "yes")},
	}
	_, err = store.AppendSpans(spans)
	require.NoError(t, err)

	// Logs: a payload record per unspanned frame, a duplicate publication
	// (first wins), a malformed payload, and ordinary log text.
	frameC := &callpbv1.Call{Digest: "xxh3:cccc", Field: "directory"}
	frameD := &callpbv1.Call{Digest: "xxh3:dddd", Field: "withSkills", ReceiverDigest: "xxh3:aaaa"}
	malformed := callPayloadLog(t, frameC)
	malformed.Body = []byte("not a proto")
	logs := []Log{
		{SpanID: validString("span"), Body: []byte("text")},
		callPayloadLog(t, frameC),
		callPayloadLog(t, frameD),
		callPayloadLog(t, frameC),
		malformed,
	}
	_, err = store.AppendLogs(logs)
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"xxh3:aaaa", "xxh3:cccc", "xxh3:dddd"}, keysOf(store.CallDigests()))

	frames, err := store.SelectCallFrames(ctx, map[string]struct{}{
		"xxh3:aaaa": {}, "xxh3:bbbb": {}, "xxh3:cccc": {}, "xxh3:dddd": {}, "xxh3:never": {},
	})
	require.NoError(t, err)
	require.Len(t, frames, 3)

	// Span-carried: the newest snapshot row.
	require.Equal(t, "xxh3:aaaa", frames[0].Digest)
	require.Nil(t, frames[0].Log)
	require.NotNil(t, frames[0].Span)
	require.Equal(t, int64(4), frames[0].Span.ID)

	// Log-carried: the first publication, decodable back to the frame.
	require.Equal(t, "xxh3:cccc", frames[1].Digest)
	require.Nil(t, frames[1].Span)
	require.NotNil(t, frames[1].Log)
	require.Equal(t, int64(2), frames[1].Log.ID)
	decoded, err := CallPayloadBody(*frames[1].Log)
	require.NoError(t, err)
	require.Equal(t, "directory", decoded.GetField())

	require.Equal(t, "xxh3:dddd", frames[2].Digest)
	require.Equal(t, int64(3), frames[2].Log.ID)
}

func TestListStringAttr(t *testing.T) {
	digest := telemetry.DagDigestAttr
	intAttr := &otlpcommonv1.KeyValue{Key: digest, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_IntValue{IntValue: 1}}}
	notString, err := MarshalProtoList([]*otlpcommonv1.KeyValue{intAttr})
	require.NoError(t, err)
	for _, tc := range []struct {
		name  string
		attrs []byte
		want  string
		ok    bool
	}{
		{"present", stringAttrsList(t, digest, "xxh3:abc"), "xxh3:abc", true},
		{"key bytes inside an earlier value", stringAttrsList(t, "x", digest, digest, "xxh3:abc"), "xxh3:abc", true},
		{"key bytes only inside a value", stringAttrsList(t, "x", digest, "y", "z"), "", false},
		{"absent", stringAttrsList(t, "x", "y"), "", false},
		{"not a string", notString, "", false},
		{"empty", nil, "", false},
		{"truncated", stringAttrsList(t, digest, "xxh3:abc")[:5], "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := listStringAttr(tc.attrs, digest)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}
