package dagui

import (
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

func TestMediaRecord(t *testing.T) {
	rec := newTestLogRecord(trace.TraceID{1}, trace.SpanID{1}, "[image: image/png]\n",
		attribute.String(telemetryattrs.LogMediaKindAttr, "image"),
		attribute.String(telemetryattrs.LogMediaMIMETypeAttr, "image/png"),
		attribute.String(telemetryattrs.LogMediaDataAttr, "aW1hZ2U="),
	)
	t.Run("ordered renderable records retain safe fallback", func(t *testing.T) {
		var before, after sdklog.Record
		before.SetBody(attribute.StringValue("before\n"))
		after.SetBody(attribute.StringValue("after\n"))
		db := NewDB()
		logs := db.IngestLogs([]sdklog.Record{before, rec, after})
		require.Len(t, logs, 3)
		var text string
		for _, record := range logs {
			body, ok := LogBodyString(record)
			require.True(t, ok)
			text += body
		}
		require.Equal(t, "before\n[image: image/png]\nafter\n", text)
		media, ok := ParseMediaRecord(logs[1])
		require.True(t, ok)
		require.Equal(t, MediaRecord{Kind: "image", MIMEType: "image/png", Data: "aW1hZ2U="}, media)
		require.True(t, db.SpanHasLogs(SpanID{SpanID: rec.SpanID()}))
	})
	for name, attr := range map[string]attribute.KeyValue{
		"wrong kind type":    attribute.Int(telemetryattrs.LogMediaKindAttr, 1),
		"unknown kind":       attribute.String(telemetryattrs.LogMediaKindAttr, "video"),
		"wrong MIME type":    attribute.Bool(telemetryattrs.LogMediaMIMETypeAttr, true),
		"mismatched MIME":    attribute.String(telemetryattrs.LogMediaMIMETypeAttr, "audio/wav"),
		"empty MIME subtype": attribute.String(telemetryattrs.LogMediaMIMETypeAttr, "image/"),
		"wrong data type":    attribute.ByteSlice(telemetryattrs.LogMediaDataAttr, []byte("image")),
		"missing data":       attribute.String(telemetryattrs.LogMediaDataAttr, ""),
	} {
		t.Run(name, func(t *testing.T) {
			invalid := rec.Clone()
			invalid.AddAttributes(attr)
			_, ok := ParseMediaRecord(invalid)
			require.False(t, ok)
			body, ok := LogBodyString(invalid)
			require.True(t, ok)
			require.Equal(t, "[image: image/png]\n", body)
		})
	}
	for _, body := range []attribute.Value{attribute.StringValue(""), attribute.ByteSliceValue([]byte("data"))} {
		invalid := rec.Clone()
		invalid.SetBody(body)
		_, ok := ParseMediaRecord(invalid)
		require.False(t, ok, "EOF and structured bodies must not render media")
	}
}
