package clientdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

const selectionTrace = "01000000000000000000000000000000"

func selectionLog(t *testing.T, span string, body *commonpb.AnyValue, attrs ...*commonpb.KeyValue) Log {
	t.Helper()
	encoded, err := MarshalProtoJSONs(attrs)
	require.NoError(t, err)
	payload, err := proto.Marshal(body)
	require.NoError(t, err)
	return Log{TraceID: sql.NullString{String: selectionTrace, Valid: true}, SpanID: sql.NullString{String: span, Valid: true}, Body: payload, Attributes: encoded}
}
func selectionText(s string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
}
func selectionAttr(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: selectionText(v)}
}

func TestArchiveSelectedLogsCutClassesAndReopen(t *testing.T) {
	root := t.TempDir()
	db, err := openStore(t.Context(), root, "client", telemetryTailBudget)
	require.NoError(t, err)
	text := selectionLog(t, "span", selectionText("text"))
	call := selectionLog(t, "span", &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: []byte{1}}}, selectionAttr(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType))
	name := selectionLog(t, "span", selectionText("renamed"), selectionAttr(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName))
	media := selectionLog(t, "span", selectionText("image"), selectionAttr(telemetryattrs.LogMediaKindAttr, "image"))
	control := selectionLog(t, "span", selectionText("control"), selectionAttr(agentcontrol.VersionAttr, "1"))
	other := selectionLog(t, "other", selectionText("not selected"))
	rows := []Log{text, call, name, media, control, other}
	for i := range rows {
		rows[i].Timestamp = int64(i + 1)
	}
	_, err = db.AppendLogs(rows)
	require.NoError(t, err)
	cut, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	_, err = db.AppendLogs([]Log{text})
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = openStore(t.Context(), root, "client", telemetryTailBudget)
	require.NoError(t, err)
	defer db.Close()
	for _, tc := range []struct {
		class string
		want  []int64
	}{
		{archive.LogRecordsAll, []int64{1, 2, 3, 4}}, {archive.LogRecordsLogs, []int64{1, 4}}, {archive.LogRecordsCallPayloads, []int64{2}}, {archive.LogRecordsMetadata, []int64{2, 3}},
	} {
		t.Run(tc.class, func(t *testing.T) {
			var ids []int64
			cursor := int64(0)
			for cursor < cut.Logs {
				batch, next, err := db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{AfterID: cursor, ThroughID: cut.Logs, Limit: 2}, selectionTrace, map[string]bool{"span": true}, &archive.LogSelection{Records: tc.class}, nil)
				require.NoError(t, err)
				require.Greater(t, next, cursor)
				cursor = next
				for _, row := range batch {
					ids = append(ids, row.ID)
				}
			}
			require.Equal(t, tc.want, ids)
		})
	}
	after := time.Unix(0, 1)
	batch, next, err := db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 99}, selectionTrace, nil, &archive.LogSelection{Records: archive.LogRecordsLogs, After: &after}, map[int64]bool{4: true})
	require.NoError(t, err)
	require.Equal(t, cut.Logs, next)
	require.Len(t, batch, 1)
	require.EqualValues(t, 6, batch[0].ID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = db.SelectArchiveLogsRange(ctx, SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 2}, selectionTrace, nil, nil, nil)
	require.ErrorIs(t, err, context.Canceled)
	_, _, err = db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: 100, Limit: 2}, selectionTrace, nil, nil, nil)
	require.ErrorContains(t, err, "truncated")
}

func TestArchiveLogBatchesBoundBytesAndSkipBodies(t *testing.T) {
	db, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	defer db.Close()
	var rows []Log
	for range 20 {
		rows = append(rows, selectionLog(t, "other", selectionText(strings.Repeat("x", 600<<10))))
	}
	rows = append(rows, selectionLog(t, "selected", selectionText("tiny")))
	_, err = db.AppendLogs(rows)
	require.NoError(t, err)
	cut, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	decode := db.logs.codec.decode
	decoded := 0
	db.logs.codec.decode = func(p []byte) (Log, error) { decoded++; return decode(p) }
	batch, next, err := db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 100}, selectionTrace, map[string]bool{"selected": true}, nil, nil)
	require.NoError(t, err)
	require.Len(t, batch, 1)
	require.Equal(t, cut.Logs, next)
	require.Equal(t, 1, decoded, "unrelated large bodies must not be decoded")
	batch, next, err = db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 100}, selectionTrace, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, batch, 1)
	require.EqualValues(t, 1, next, "batch must stop at byte budget before reading another large body")
}

func TestArchiveSpanPrioritySubtreeAndCut(t *testing.T) {
	db, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	defer db.Close()
	var spans []Span
	for i := 1; i <= 8; i++ {
		row := Span{TraceID: selectionTrace, SpanID: fmt.Sprint(i), Attributes: []byte("[]"), Links: []byte("[]")}
		if i > 1 {
			row.ParentSpanID = sql.NullString{String: fmt.Sprint(i - 1), Valid: true}
		}
		if i == 6 {
			row.StatusCode = 1
		}
		spans = append(spans, row)
	}
	// latest pre-cut snapshot wins; later updates must not move the cut.
	_, err = db.AppendSpans(spans)
	require.NoError(t, err)
	_, err = db.AppendLogs([]Log{selectionLog(t, "3", selectionText("output"))})
	require.NoError(t, err)
	cut, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	after := spans[0]
	after.ParentSpanID = sql.NullString{String: "8", Valid: true}
	_, err = db.AppendSpans([]Span{after})
	require.NoError(t, err)
	v, err := db.ArchiveSpanView(t.Context(), selectionTrace, cut, &archive.SpanSelection{DagUIView: true})
	require.NoError(t, err)
	require.True(t, v.partial)
	require.True(t, v.selected["6"])
	require.True(t, v.selected["1"])
	require.False(t, v.selected["8"])
	require.True(t, v.nodes["3"].hasLogs)
	require.EqualValues(t, 1, v.nodes["1"].row)
	require.EqualValues(t, 1, v.Attributes("3")[0].Value.GetIntValue())
	v, err = db.ArchiveSpanView(t.Context(), selectionTrace, cut, &archive.SpanSelection{NoRoot: true, Listen: []string{"3"}})
	require.NoError(t, err)
	require.Len(t, v.Scope("3", true), 6)
	require.Len(t, v.Scope("3", false), 1)
	require.True(t, v.selected["8"])
}
