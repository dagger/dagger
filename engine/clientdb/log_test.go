package clientdb

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
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

// sampleLogRows are log rows as the store holds them: under two resources
// (one with a schema URL) and two scopes, with full and bare records, a call
// payload's bytes body, and rows LogsToPB drops.
func sampleLogRows(t *testing.T) []Log {
	t.Helper()
	enc := func(m proto.Message) []byte {
		t.Helper()
		b, err := proto.Marshal(m)
		require.NoError(t, err)
		return b
	}
	list := func(kvs ...*otlpcommonv1.KeyValue) []byte {
		t.Helper()
		b, err := MarshalProtoList(kvs)
		require.NoError(t, err)
		return b
	}
	str := func(v string) []byte {
		return enc(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: v}})
	}
	resA := enc(&otlpresourcev1.Resource{Attributes: []*otlpcommonv1.KeyValue{strAttr("service.name", "dagger-engine"), strAttr("a", "1")}})
	resB := enc(&otlpresourcev1.Resource{Attributes: []*otlpcommonv1.KeyValue{strAttr("service.name", "dagger-cli")}})
	scopeX := enc(&otlpcommonv1.InstrumentationScope{Name: "dagger.io/engine", Version: "v1"})
	scopeY := enc(&otlpcommonv1.InstrumentationScope{Name: "dagger.io/calls", Attributes: []*otlpcommonv1.KeyValue{strAttr("k", "v")}})
	ids := func(row Log, span string) Log {
		row.TraceID = sql.NullString{String: "0102030405060708090a0b0c0d0e0f10", Valid: true}
		row.SpanID = sql.NullString{String: span, Valid: true}
		return row
	}
	return []Log{
		ids(Log{
			Timestamp: 1_700_000_000_000_000_123, SeverityNumber: 9, SeverityText: "INFO",
			Body:       str("naïve ☃ \x00 output\n"),
			Attributes: list(sampleSpanAttrs()...),
			Resource:   resA, InstrumentationScope: scopeX,
		}, "1112131415161718"),
		// No timestamp, severity, body or attributes.
		ids(Log{Resource: resA, InstrumentationScope: scopeX}, "2122232425262728"),
		// A call payload, under the other resource with a schema URL.
		ids(Log{
			Timestamp:  5,
			Body:       enc(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: []byte{0xfb, 0x00, 0x7f}}}),
			Attributes: list(strAttr("dagger.io/content.type", "application/x-call"), strAttr("dagger.io/call.digest", "xxh3:1")),
			Resource:   resB, ResourceSchemaURL: "https://opentelemetry.io/schemas/1.26.0", InstrumentationScope: scopeY,
		}, "3132333435363738"),
		// A negative severity, as an int64 column can hold.
		ids(Log{SeverityNumber: -1, Body: str("neg"), Resource: resA, InstrumentationScope: scopeY}, "4142434445464748"),
		// Dropped: no trace or span ID.
		{Body: str("no ids"), Resource: resA, InstrumentationScope: scopeX},
		// Dropped: a span ID that isn't hex.
		ids(Log{Body: str("bad id"), Resource: resA, InstrumentationScope: scopeX}, "zz"),
		// Dropped: attributes whose elements can't be split.
		ids(Log{Body: str("bad attrs"), Attributes: []byte{0xff}, Resource: resA, InstrumentationScope: scopeX}, "5152535455565758"),
		// Dropped: a resource that can't be decoded.
		ids(Log{Body: str("bad resource"), Resource: []byte{0xff}, InstrumentationScope: scopeX}, "6162636465666768"),
		// The first resource and scope again, after the others.
		ids(Log{Timestamp: 7, Body: str("again"), Resource: resA, InstrumentationScope: scopeX}, "7172737475767778"),
	}
}

// TestAppendLogsRequestPB checks that the log stream's encoding written from
// stored rows is byte for byte what proto.Marshal makes of the request
// LogsToPB builds from the same rows.
func TestAppendLogsRequestPB(t *testing.T) {
	rows := sampleLogRows(t)
	want, err := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: LogsToPB(rows)})
	require.NoError(t, err)
	got, err := AppendLogsRequestPB(nil, rows)
	require.NoError(t, err)
	require.Equal(t, want, got)

	var req collogspb.ExportLogsServiceRequest
	require.NoError(t, proto.Unmarshal(got, &req))
	require.Len(t, req.ResourceLogs, 2, "both resources are written")

	// Into a buffer that already holds bytes, it appends.
	prefix := []byte("prefix")
	got, err = AppendLogsRequestPB(slices.Clone(prefix), rows)
	require.NoError(t, err)
	require.Equal(t, append(slices.Clone(prefix), want...), got)

	got, err = AppendLogsRequestPB(nil, nil)
	require.NoError(t, err)
	require.Empty(t, got)

	// Like proto.Marshal, it fails on a severity text that isn't UTF-8.
	bad := rows[0]
	bad.SeverityText = "\xff"
	_, protoErr := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: LogsToPB([]Log{bad})})
	require.Error(t, protoErr)
	_, err = AppendLogsRequestPB(nil, append(slices.Clone(rows), bad))
	require.Error(t, err)

	// And on the schema URL of a resource no other row has, that isn't
	// UTF-8. A resource already grouped keeps its first row's schema URL.
	bad = rows[0]
	bad.ResourceSchemaURL = "\xff"
	_, protoErr = proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: LogsToPB([]Log{bad})})
	require.Error(t, protoErr)
	_, err = AppendLogsRequestPB(nil, []Log{bad})
	require.Error(t, err)
	got, err = AppendLogsRequestPB(nil, append(slices.Clone(rows), bad))
	require.NoError(t, err, "the bad schema URL is superseded by the resource's first")
	want, err = proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: LogsToPB(append(slices.Clone(rows), bad))})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestAppendLogsRequestPBAllocs checks that the log stream encodes a batch
// without decoding each record into messages and encoding those again,
// which cost about 69 allocations per record of this shape.
func TestAppendLogsRequestPBAllocs(t *testing.T) {
	sample := sampleLogRows(t)
	batch := func(n int) []Log {
		rows := make([]Log, n)
		for i := range rows {
			rows[i] = sample[0]
			rows[i].SpanID.String = fmt.Sprintf("%016x", i+1)
		}
		return rows
	}
	small, large := batch(1), batch(1000)
	buf := make([]byte, 0, 4<<20)
	allocs := func(rows []Log) float64 {
		return testing.AllocsPerRun(20, func() {
			if _, err := AppendLogsRequestPB(buf[:0], rows); err != nil {
				t.Fatal(err)
			}
		})
	}
	smallAllocs, largeAllocs := allocs(small), allocs(large)
	t.Logf("allocations: %v for 1 record, %v for %d", smallAllocs, largeAllocs, len(large))
	// Only the per-batch slices of records grow with the batch.
	require.Less(t, largeAllocs-smallAllocs, float64(len(large))/20)
}
