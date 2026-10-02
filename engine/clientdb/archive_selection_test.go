package clientdb

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
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
				batch, next, err := db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{AfterID: cursor, ThroughID: cut.Logs, Limit: 2}, selectionTrace, map[string]bool{"span": true}, &archive.LogSelection{Records: tc.class}, false)
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
	// An unsealed archive has no bootstrap, so its history carries control.
	batch, next, err := db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 99}, selectionTrace, map[string]bool{"span": true}, nil, true)
	require.NoError(t, err)
	require.Equal(t, cut.Logs, next)
	require.Len(t, batch, 5)
	require.EqualValues(t, 5, batch[4].ID)
	after := time.Unix(0, 1)
	batch, next, err = db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 99}, selectionTrace, nil, &archive.LogSelection{Records: archive.LogRecordsLogs, After: &after}, false)
	require.NoError(t, err)
	require.Equal(t, cut.Logs, next)
	require.Len(t, batch, 2)
	require.EqualValues(t, 4, batch[0].ID)
	require.EqualValues(t, 6, batch[1].ID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = db.SelectArchiveLogsRange(ctx, SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 2}, selectionTrace, nil, nil, false)
	require.ErrorIs(t, err, context.Canceled)
	_, _, err = db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: 100, Limit: 2}, selectionTrace, nil, nil, false)
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
	batch, next, err := db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 100}, selectionTrace, map[string]bool{"selected": true}, nil, false)
	require.NoError(t, err)
	require.Len(t, batch, 1)
	require.Equal(t, cut.Logs, next)
	require.Equal(t, 1, decoded, "unrelated large bodies must not be decoded")
	batch, next, err = db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 100}, selectionTrace, nil, nil, false)
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
	_, err = db.ArchiveSpanView(t.Context(), selectionTrace, HighWater{Logs: -1}, nil)
	require.ErrorContains(t, err, "invalid archive cut")
	batch, next, err := db.SelectArchiveLogsRange(t.Context(), SelectLogsRangeParams{AfterID: 1, ThroughID: 1, Limit: math.MaxInt64}, selectionTrace, nil, nil, false)
	require.NoError(t, err)
	require.Empty(t, batch)
	require.EqualValues(t, 1, next)
}

func TestArchivePriorityChecksAndPassthrough(t *testing.T) {
	db, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	defer db.Close()
	var rows []Span
	for i := 1; i <= 7; i++ {
		row := Span{TraceID: selectionTrace, SpanID: fmt.Sprint(i), Attributes: []byte("[]"), Links: []byte("[]")}
		if i > 1 {
			row.ParentSpanID = sql.NullString{String: fmt.Sprint(i - 1), Valid: true}
		}
		if i == 2 || i == 3 {
			key := telemetry.UIInternalAttr
			if i == 3 {
				key = telemetry.UIPassthroughAttr
			}
			row.Attributes, err = MarshalProtoJSONs([]*commonpb.KeyValue{{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}}})
			require.NoError(t, err)
		}
		if i == 5 {
			row.Attributes, err = MarshalProtoJSONs([]*commonpb.KeyValue{selectionAttr(telemetry.CheckNameAttr, "check")})
			require.NoError(t, err)
		}
		rows = append(rows, row)
	}
	_, err = db.AppendSpans(rows)
	require.NoError(t, err)
	cut, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	v, err := db.ArchiveSpanView(t.Context(), selectionTrace, cut, &archive.SpanSelection{})
	require.NoError(t, err)
	require.True(t, v.selected["4"], "initial view crosses hidden and passthrough spans")
	require.True(t, v.selected["5"], "checks are priority even without an error")
	require.False(t, v.selected["7"])
}

func TestArchivePriorityConversation(t *testing.T) {
	db, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	defer db.Close()
	boolAttr := func(k string) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}}
	}
	agentID := selectionAttr(string(semconv.GenAIAgentIDKey), "agent-1")
	attrs := map[int][]*commonpb.KeyValue{
		3: {boolAttr(telemetryattrs.AgentAttr), agentID},
		6: {selectionAttr(telemetry.LLMRoleAttr, "user"), agentID},
		// A synchronous LLM conversation has no agent identity.
		9: {selectionAttr(telemetry.LLMRoleAttr, "assistant")},
	}
	var rows []Span
	for i := 1; i <= 9; i++ {
		row := Span{TraceID: selectionTrace, SpanID: fmt.Sprint(i), Attributes: []byte("[]"), Links: []byte("[]")}
		if i > 1 {
			row.ParentSpanID = sql.NullString{String: fmt.Sprint(i - 1), Valid: true}
		}
		if a, ok := attrs[i]; ok {
			row.Attributes, err = MarshalProtoJSONs(a)
			require.NoError(t, err)
		}
		rows = append(rows, row)
	}
	_, err = db.AppendSpans(rows)
	require.NoError(t, err)
	cut, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	v, err := db.ArchiveSpanView(t.Context(), selectionTrace, cut, &archive.SpanSelection{DagUIView: true})
	require.NoError(t, err)
	require.True(t, v.partial)
	require.True(t, v.selected["3"], "agent loops are priority")
	require.True(t, v.selected["5"], "a message's chain to its loop is loaded")
	require.True(t, v.selected["6"], "messages are priority")
	require.True(t, v.selected["7"], "a message's children are counted and shown")
	require.False(t, v.selected["8"])
	require.False(t, v.selected["9"], "only agent conversation spans are priority")
}

func TestArchiveMetadataTextAndVisibility(t *testing.T) {
	for _, key := range []string{telemetry.LogsGlobalAttr, telemetry.LogsVerboseAttr} {
		row := selectionLog(t, "span", selectionText("hidden"), &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}})
		m := archiveLogMetadata(row)
		require.NoError(t, m.err)
		require.False(t, m.text)
		require.False(t, m.metadata)
	}
	for _, key := range []string{telemetryattrs.ProgressItemAttr, telemetryattrs.AgentStateAttr, telemetryattrs.AgentSnapshotDigestAttr, telemetryattrs.LogRoleAttr} {
		m := archiveLogMetadata(selectionLog(t, "span", selectionText("semantic"), selectionAttr(key, "value")))
		require.NoError(t, m.err)
		require.False(t, m.text)
		require.True(t, m.metadata)
	}
}
