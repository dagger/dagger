package dagui

import (
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

func TestMediaRecord(t *testing.T) {
	rec := newTestLogRecord(trace.TraceID{1}, trace.SpanID{1}, "[image: image/png]\n",
		otellog.String(telemetryattrs.LogMediaKindAttr, "image"),
		otellog.String(telemetryattrs.LogMediaMIMETypeAttr, "image/png"),
		otellog.String(telemetryattrs.LogMediaDataAttr, "aW1hZ2U="),
	)
	t.Run("ordered renderable records retain safe fallback", func(t *testing.T) {
		var before, after sdklog.Record
		before.SetBody(otellog.StringValue("before\n"))
		after.SetBody(otellog.StringValue("after\n"))
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
		require.True(t, db.Spans.Map[SpanID{SpanID: rec.SpanID()}].HasLogs)
	})
	for name, attr := range map[string]otellog.KeyValue{
		"wrong kind type":    otellog.Int(telemetryattrs.LogMediaKindAttr, 1),
		"unknown kind":       otellog.String(telemetryattrs.LogMediaKindAttr, "video"),
		"wrong MIME type":    otellog.Bool(telemetryattrs.LogMediaMIMETypeAttr, true),
		"mismatched MIME":    otellog.String(telemetryattrs.LogMediaMIMETypeAttr, "audio/wav"),
		"empty MIME subtype": otellog.String(telemetryattrs.LogMediaMIMETypeAttr, "image/"),
		"wrong data type":    otellog.Bytes(telemetryattrs.LogMediaDataAttr, []byte("image")),
		"missing data":       otellog.String(telemetryattrs.LogMediaDataAttr, ""),
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
	for _, body := range []otellog.Value{otellog.StringValue(""), otellog.BytesValue([]byte("data"))} {
		invalid := rec.Clone()
		invalid.SetBody(body)
		_, ok := ParseMediaRecord(invalid)
		require.False(t, ok, "EOF and structured bodies must not render media")
	}
}
