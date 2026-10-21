package clientdb

import (
	"database/sql"
	"reflect"
	"testing"
	"unsafe"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

// referenceArchiveLogMeta is the original, uncompacted classification, kept
// as the oracle the compact index must match exactly.
type referenceArchiveLogMeta struct {
	trace, span                             string
	text, nonempty, call, metadata, control bool
	err                                     error
}

func referenceArchiveLogMetadata(row Log) referenceArchiveLogMeta {
	m := referenceArchiveLogMeta{trace: row.TraceID.String, span: row.SpanID.String}
	var attrs []*commonpb.KeyValue
	if err := UnmarshalProtoJSONs(row.Attributes, &commonpb.KeyValue{}, &attrs); err != nil {
		m.err = err
		return m
	}
	var hidden bool
	for _, a := range attrs {
		switch a.GetKey() {
		case telemetry.LogsGlobalAttr, telemetry.LogsVerboseAttr:
			hidden = hidden || a.GetValue().GetBoolValue()
		case agentcontrol.VersionAttr:
			m.control = true
		case telemetryattrs.LogRoleAttr, telemetryattrs.ProgressItemAttr, telemetryattrs.AgentStateAttr, telemetryattrs.AgentSnapshotDigestAttr:
			m.metadata = true
		case telemetry.ContentTypeAttr:
			m.call = a.GetValue().GetStringValue() == telemetryattrs.CallPayloadContentType
			if m.call {
				m.metadata = true
			}
		}
	}
	var text, nonempty bool
	for body := row.Body; len(body) > 0; {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			m.err = protowire.ParseError(n)
			return m
		}
		body = body[n:]
		n = protowire.ConsumeFieldValue(num, typ, body)
		if n < 0 {
			m.err = protowire.ParseError(n)
			return m
		}
		if num >= 1 && num <= 7 {
			text = num == 1 && typ == protowire.BytesType
			if text {
				value, _ := protowire.ConsumeBytes(body)
				nonempty = len(value) > 0
			}
		}
		body = body[n:]
	}
	m.text = text && !m.metadata && !m.control && !hidden
	m.nonempty = nonempty
	if !text {
		m.metadata = true
	}
	return m
}

func TestArchiveLogMetaIsCompactAndPointerFree(t *testing.T) {
	require.LessOrEqual(t, unsafe.Sizeof(archiveLogMeta{}), uintptr(32))
	typ := reflect.TypeFor[archiveLogMeta]()
	for i := range typ.NumField() {
		switch typ.Field(i).Type.Kind() {
		case reflect.Int64, reflect.Uint32, reflect.Uint8:
		default:
			t.Fatalf("archiveLogMeta.%s is %s; the per-row index must stay pointer-free", typ.Field(i).Name, typ.Field(i).Type)
		}
	}
}

// TestArchiveLogIndexMatchesReference feeds the index rows covering every
// class, repeated (memo hits) and with malformed attributes and bodies, and
// requires the compact index to decode back to the original classification.
func TestArchiveLogIndexMatchesReference(t *testing.T) {
	boolAttr := func(k string, v bool) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v}}}
	}
	bytesBody := &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: []byte{1}}}
	rows := []Log{
		selectionLog(t, "a", selectionText("text")),
		selectionLog(t, "a", selectionText("")),
		selectionLog(t, "b", bytesBody),
		selectionLog(t, "b", selectionText("hidden"), boolAttr(telemetry.LogsGlobalAttr, true)),
		selectionLog(t, "b", selectionText("not hidden"), boolAttr(telemetry.LogsVerboseAttr, false)),
		selectionLog(t, "b", selectionText("control"), selectionAttr(agentcontrol.VersionAttr, "1")),
		selectionLog(t, "c", bytesBody, selectionAttr(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType)),
		// The last content type decides call; metadata sticks.
		selectionLog(t, "c", bytesBody, selectionAttr(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType), selectionAttr(telemetry.ContentTypeAttr, "text/plain")),
		selectionLog(t, "c", selectionText("md"), selectionAttr(telemetry.ContentTypeAttr, "text/markdown")),
		selectionLog(t, "c", selectionText("role"), selectionAttr(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName)),
		selectionLog(t, "c", selectionText("progress"), selectionAttr(telemetryattrs.ProgressItemAttr, "x")),
		{TraceID: sql.NullString{String: selectionTrace, Valid: true}, SpanID: sql.NullString{String: "d", Valid: true}, Body: []byte{0x0a}, Attributes: []byte("[]")},
		{TraceID: sql.NullString{String: selectionTrace, Valid: true}, SpanID: sql.NullString{String: "d", Valid: true}, Attributes: []byte(`[{"nope":1}]`)},
		{TraceID: sql.NullString{String: selectionTrace, Valid: true}, SpanID: sql.NullString{String: "d", Valid: true}},
		{SpanID: sql.NullString{String: "e", Valid: true}, Attributes: []byte("[]")},
	}
	// Repeat every row so the second pass hits the attribute memo.
	rows = append(rows, rows...)
	idx := newLogLookup()
	for i := range rows {
		rows[i].ID = int64(i + 1)
	}
	idx.addAll(rows[:7])
	for _, row := range rows[7:] {
		idx.add(row)
	}
	require.Len(t, idx.archive, len(rows))
	for i, row := range rows {
		want := referenceArchiveLogMetadata(row)
		m := idx.archive[i]
		got := referenceArchiveLogMeta{
			trace:    idx.traceIDs[m.trace],
			span:     idx.spanIDs[m.span],
			text:     m.flags.has(archiveLogText),
			nonempty: m.flags.has(archiveLogNonempty),
			call:     m.flags.has(archiveLogCall),
			metadata: m.flags.has(archiveLogMetadata),
			control:  m.flags.has(archiveLogControl),
			err:      idx.archiveErrs[int64(i)],
		}
		require.Equal(t, want.err != nil, m.flags.has(archiveLogFailed), "row %d", i)
		if want.err != nil {
			require.EqualError(t, got.err, want.err.Error(), "row %d", i)
			want.err, got.err = nil, nil
		}
		require.Equal(t, want, got, "row %d", i)
	}
	require.Len(t, idx.archiveErrs, 6, "only failed rows have errors")
	require.Len(t, idx.traceIDs, 2)
	require.Len(t, idx.spanIDs, 5)
}

func TestArchiveLogsSurfaceMalformedRows(t *testing.T) {
	db, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	defer db.Close()
	bad := selectionLog(t, "bad", selectionText("x"))
	bad.Attributes = []byte(`[{"nope":1}]`)
	_, err = db.AppendLogs([]Log{selectionLog(t, "good", selectionText("ok")), bad})
	require.NoError(t, err)
	cut, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	rows, next, err := db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 10}, selectionTrace, map[string]bool{"good": true}, nil, false)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, cut.Logs, next)
	_, _, err = db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 10}, selectionTrace, nil, nil, false)
	require.ErrorContains(t, err, "protojson")
	rows, _, err = db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 10}, "02000000000000000000000000000000", nil, nil, false)
	require.NoError(t, err)
	require.Empty(t, rows, "an unknown trace matches nothing")
}
