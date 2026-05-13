package clientdb

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestLogsToPBAllowsSchemalessResources(t *testing.T) {
	body, err := proto.Marshal(&otlpcommonv1.AnyValue{
		Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "hello"},
	})
	require.NoError(t, err)

	attrs, err := MarshalProtoJSONs([]*otlpcommonv1.KeyValue{
		{
			Key: "stdio.stream",
			Value: &otlpcommonv1.AnyValue{
				Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "stdout"},
			},
		},
	})
	require.NoError(t, err)

	scope, err := protojson.Marshal(&otlpcommonv1.InstrumentationScope{Name: "test-logger"})
	require.NoError(t, err)

	resource, err := protojson.Marshal(&otlpresourcev1.Resource{
		Attributes: []*otlpcommonv1.KeyValue{
			{
				Key: "service.name",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "vitest"},
				},
			},
		},
	})
	require.NoError(t, err)

	traceID, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("1112131415161718")
	require.NoError(t, err)

	logs := LogsToPB([]Log{
		{
			TraceID: sql.NullString{
				String: traceID.String(),
				Valid:  true,
			},
			SpanID: sql.NullString{
				String: spanID.String(),
				Valid:  true,
			},
			Timestamp:            123,
			SeverityNumber:       1,
			SeverityText:         "TRACE",
			Body:                 body,
			Attributes:           attrs,
			InstrumentationScope: scope,
			Resource:             resource,
			ResourceSchemaUrl:    "",
		},
	})

	require.Len(t, logs, 1)
	require.Empty(t, logs[0].SchemaUrl)
	require.Len(t, logs[0].Resource.Attributes, 1)
	require.Equal(t, "service.name", logs[0].Resource.Attributes[0].Key)
	require.Equal(t, "vitest", logs[0].Resource.Attributes[0].Value.GetStringValue())
	require.Len(t, logs[0].ScopeLogs, 1)
	require.Empty(t, logs[0].ScopeLogs[0].SchemaUrl)
	require.Len(t, logs[0].ScopeLogs[0].LogRecords, 1)

	record := logs[0].ScopeLogs[0].LogRecords[0]
	require.Equal(t, "hello", record.Body.GetStringValue())
	require.Equal(t, traceID[:], record.TraceId)
	require.Equal(t, spanID[:], record.SpanId)
}
