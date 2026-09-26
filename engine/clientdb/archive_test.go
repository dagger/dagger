package clientdb

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	logapi "go.opentelemetry.io/otel/log"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

func TestArchiveCheckpointFsyncFailureAndReopen(t *testing.T) {
	root := t.TempDir()
	db, err := openStore(t.Context(), root, "client", telemetryTailBudget)
	require.NoError(t, err)
	_, err = db.AppendLogs([]Log{{Body: []byte("first")}})
	require.NoError(t, err)
	syncErr := errors.New("fsync unavailable")
	db.logs.spill.testSyncHook = func() error { return syncErr }
	_, err = db.Checkpoint(t.Context())
	require.ErrorIs(t, err, syncErr)
	// A failed fsync is not persistence success, but the next barrier can retry.
	db.logs.spill.testSyncHook = nil
	cut, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, 1, cut.Logs)
	_, err = db.AppendLogs([]Log{{Body: []byte("after cut")}})
	require.NoError(t, err)
	rows, err := db.SelectLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 100})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NoError(t, db.Close())
	reopened, err := openStore(t.Context(), root, "client", telemetryTailBudget)
	require.NoError(t, err)
	defer reopened.Close()
	rows, err = reopened.SelectLogsRange(t.Context(), SelectLogsRangeParams{ThroughID: cut.Logs, Limit: 100})
	require.NoError(t, err)
	require.Equal(t, []byte("first"), rows[0].Body)
}

func TestArchiveIndexRevisionAndReopening(t *testing.T) {
	const traceID = "01000000000000000000000000000000"
	root := t.TempDir()
	db, err := openStore(t.Context(), root, "client", telemetryTailBudget)
	require.NoError(t, err)
	a := agentcontrol.Agent{Key: agentcontrol.Key{Namespace: agentcontrol.Namespace{Session: "session", Trace: traceID, Incarnation: "incarnation"}, Handle: "worker"}, Revision: 9, State: "IDLE", Digest: "recipe"}
	appendAgent := func(a agentcontrol.Agent) {
		var attrs []*commonpb.KeyValue
		r := a.Record()
		r.WalkAttributes(func(kv logapi.KeyValue) bool {
			attrs = append(attrs, &commonpb.KeyValue{Key: kv.Key, Value: telemetry.LogValueToPB(kv.Value)})
			return true
		})
		encoded, err := MarshalProtoJSONs(attrs)
		require.NoError(t, err)
		body, err := proto.Marshal(telemetry.LogValueToPB(r.Body()))
		require.NoError(t, err)
		_, err = db.AppendLogs([]Log{{TraceID: sql.NullString{String: traceID, Valid: true}, Attributes: encoded, Body: body}})
		require.NoError(t, err)
	}
	appendAgent(a)
	old := a
	old.Revision = 2
	old.Digest = "obsolete"
	appendAgent(old)
	want := agentcontrol.Expectation{Agents: map[agentcontrol.Key]int64{a.Key: 9}}
	cut, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	rows, err := db.ControlRows(t.Context(), traceID, cut.Logs, want)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 1, rows[0].ID)
	require.NoError(t, db.Close())
	db, err = openStore(t.Context(), root, "client", telemetryTailBudget)
	require.NoError(t, err)
	defer db.Close()
	rows, err = db.ControlRows(t.Context(), traceID, cut.Logs, want)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	want.Agents[a.Key] = 10
	_, err = db.ControlRows(t.Context(), traceID, cut.Logs, want)
	require.Error(t, err, "latest received state is not evidence of producer completeness")
}

func TestArchiveIndexTracksLatestTitlePerTrace(t *testing.T) {
	const traceID = "01000000000000000000000000000000"
	const otherTrace = "02000000000000000000000000000000"
	root := t.TempDir()
	db, err := openStore(t.Context(), root, "client", telemetryTailBudget)
	require.NoError(t, err)
	row := func(trace string, body logapi.Value, attrs ...logapi.KeyValue) Log {
		var pbAttrs []*commonpb.KeyValue
		for _, kv := range attrs {
			pbAttrs = append(pbAttrs, &commonpb.KeyValue{Key: kv.Key, Value: telemetry.LogValueToPB(kv.Value)})
		}
		encoded, err := MarshalProtoJSONs(pbAttrs)
		require.NoError(t, err)
		data, err := proto.Marshal(telemetry.LogValueToPB(body))
		require.NoError(t, err)
		return Log{TraceID: sql.NullString{String: trace, Valid: true}, Attributes: encoded, Body: data}
	}
	spanName := logapi.String(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName)
	require.Empty(t, db.Title(traceID))
	_, err = db.AppendLogs([]Log{
		row(traceID, logapi.StringValue("first title"), spanName),
		row(traceID, logapi.StringValue("ordinary output")),
	})
	require.NoError(t, err)
	require.Equal(t, "first title", db.Title(traceID))
	_, err = db.AppendLogs([]Log{
		// Another trace's title never becomes this trace's title.
		row(otherTrace, logapi.StringValue("foreign title"), spanName),
		// Other roles, non-string bodies and blank titles are ignored.
		row(traceID, logapi.StringValue("not a title"), logapi.String(telemetryattrs.LogRoleAttr, "other")),
		row(traceID, logapi.IntValue(3), spanName),
		row(traceID, logapi.StringValue("  "), spanName),
		{TraceID: sql.NullString{String: traceID, Valid: true}, Attributes: []byte(telemetryattrs.LogRoleAttr + " malformed"), Body: []byte("x")},
	})
	require.NoError(t, err)
	require.Equal(t, "first title", db.Title(traceID))
	require.Equal(t, "foreign title", db.Title(otherTrace))
	// A regenerated title supersedes the earlier one.
	_, err = db.AppendLogs([]Log{row(traceID, logapi.StringValue("regenerated"), spanName)})
	require.NoError(t, err)
	require.Equal(t, "regenerated", db.Title(traceID))
	cut, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	_, err = db.ControlRows(t.Context(), traceID, cut.Logs, agentcontrol.Expectation{Agents: map[agentcontrol.Key]int64{}})
	require.NoError(t, err, "a malformed title record is not a restore-critical failure")
	require.NoError(t, db.Close())

	reopened, err := openStore(t.Context(), root, "client", telemetryTailBudget)
	require.NoError(t, err)
	defer reopened.Close()
	require.Equal(t, "regenerated", reopened.Title(traceID), "the title index is rebuilt on reopen")
}
