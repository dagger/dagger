package clientdb

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/agentcontrol"
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

func TestArchiveCallPayload(t *testing.T) {
	const traceID = "01000000000000000000000000000000"
	db, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	defer db.Close()
	inTrace := func(row Log, trace string) Log {
		row.TraceID = sql.NullString{String: trace, Valid: true}
		return row
	}
	malformed := callPayloadLog(t, &callpbv1.Call{Digest: "xxh3:bad"})
	malformed.Body = []byte("not a proto")
	_, err = db.AppendLogs([]Log{
		inTrace(callPayloadLog(t, &callpbv1.Call{Digest: "xxh3:aaaa", Field: "first"}), traceID),
		inTrace(callPayloadLog(t, &callpbv1.Call{Digest: "xxh3:aaaa", Field: "again"}), traceID),
		inTrace(callPayloadLog(t, &callpbv1.Call{Digest: "xxh3:other"}), "02000000000000000000000000000000"),
		inTrace(malformed, traceID),
	})
	require.NoError(t, err)

	row, err := db.CallPayload(t.Context(), traceID, "xxh3:aaaa", 4)
	require.NoError(t, err)
	require.EqualValues(t, 1, row.ID, "the first publication wins")
	_, err = db.CallPayload(t.Context(), traceID, "xxh3:aaaa", 0)
	require.Error(t, err, "a row beyond the cut is not persisted")
	_, err = db.CallPayload(t.Context(), traceID, "xxh3:other", 4)
	require.Error(t, err, "a payload from another trace is not this archive's")
	_, err = db.CallPayload(t.Context(), traceID, "xxh3:bad", 4)
	require.Error(t, err)

	// A malformed payload fails only its own lookup, not the trace's control rows.
	_, err = db.ControlRows(t.Context(), traceID, 4, agentcontrol.Expectation{})
	require.NoError(t, err)
}
