package clientdb

import (
	"log/slog"
	"time"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func (dbLog *Log) Record() sdklog.Record {
	var rec sdklog.Record

	rec.SetTimestamp(time.Unix(0, dbLog.Timestamp))

	rec.SetSeverity(log.Severity(dbLog.Severity))

	if dbLog.TraceID.Valid {
		tid, _ := trace.TraceIDFromHex(dbLog.TraceID.String)
		rec.SetTraceID(tid)
	}

	if dbLog.SpanID.Valid {
		sid, _ := trace.SpanIDFromHex(dbLog.SpanID.String)
		rec.SetSpanID(sid)
	}

	if len(dbLog.Body) > 0 {
		body := &otlpcommonv1.AnyValue{}
		if err := protojson.Unmarshal(dbLog.Body, body); err != nil {
			slog.Warn("failed to unmarshal log body", "error", err)
		} else {
			rec.SetBody(telemetry.LogValueFromPB(body))
		}
	}

	if len(dbLog.Attributes) > 0 {
		var attrs []*otlpcommonv1.KeyValue
		if err := UnmarshalProtos(dbLog.Attributes, &otlpcommonv1.KeyValue{}, &attrs); err != nil {
			slog.Warn("failed to unmarshal log attributes", "error", err)
		} else {
			rec.SetAttributes(telemetry.LogKeyValuesFromPB(attrs)...)
		}
	}

	return rec
}
