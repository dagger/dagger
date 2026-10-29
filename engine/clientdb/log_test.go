package clientdb

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

func TestLogsToPBAllowsSchemalessResources(t *testing.T) {
	body, err := proto.Marshal(&otlpcommonv1.AnyValue{
		Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "hello"},
	})
	require.NoError(t, err)

	attrs, err := MarshalProtoList([]*otlpcommonv1.KeyValue{
		{
			Key: "stdio.stream",
			Value: &otlpcommonv1.AnyValue{
				Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "stdout"},
			},
		},
	})
	require.NoError(t, err)

	scope, err := proto.Marshal(&otlpcommonv1.InstrumentationScope{Name: "test-logger"})
	require.NoError(t, err)

	resource, err := proto.Marshal(&otlpresourcev1.Resource{
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

	row := Log{
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
		ResourceSchemaURL:    "",
	}
	invalid := row
	invalid.TraceID.Valid = false
	invalid.SpanID.Valid = false
	logs := LogsToPB([]Log{invalid, row})

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

func TestLogsToPBGroupsByResourceAndScope(t *testing.T) {
	marshal := func(m proto.Message) []byte {
		b, err := proto.Marshal(m)
		require.NoError(t, err)
		return b
	}
	resource := func(name string) []byte {
		return marshal(&otlpresourcev1.Resource{Attributes: []*otlpcommonv1.KeyValue{{
			Key:   "service.name",
			Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: name}},
		}}})
	}
	scope := func(name string) []byte {
		return marshal(&otlpcommonv1.InstrumentationScope{Name: name})
	}
	row := func(res, sc []byte, body string) Log {
		return Log{
			TraceID:              sql.NullString{String: "0102030405060708090a0b0c0d0e0f10", Valid: true},
			SpanID:               sql.NullString{String: "1112131415161718", Valid: true},
			Body:                 marshal(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: body}}),
			InstrumentationScope: sc,
			Resource:             res,
		}
	}
	engine, sdk := resource("engine"), resource("sdk")
	calls, logs := scope("calls"), scope("logs")
	malformed := row([]byte{0xff}, calls, "dropped")

	got := LogsToPB([]Log{
		row(engine, calls, "e1"),
		row(sdk, logs, "s1"),
		malformed,
		row(engine, logs, "e2"),
		row(engine, calls, "e3"),
		row(sdk, logs, "s2"),
	})

	bodies := map[string]map[string][]string{}
	seenScopes := map[*otlpcommonv1.InstrumentationScope]bool{}
	for _, rl := range got {
		res := rl.Resource.Attributes[0].Value.GetStringValue()
		require.NotContains(t, bodies, res, "one ResourceLogs per resource")
		bodies[res] = map[string][]string{}
		for _, sl := range rl.ScopeLogs {
			require.False(t, seenScopes[sl.Scope], "each ScopeLogs has its own scope message")
			seenScopes[sl.Scope] = true
			for _, rec := range sl.LogRecords {
				bodies[res][sl.Scope.Name] = append(bodies[res][sl.Scope.Name], rec.Body.GetStringValue())
			}
		}
	}
	require.Equal(t, map[string]map[string][]string{
		"engine": {"calls": {"e1", "e3"}, "logs": {"e2"}},
		"sdk":    {"logs": {"s1", "s2"}},
	}, bodies)
}
