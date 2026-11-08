package server

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
)

func TestClientRuntimeRetainsTelemetryStoresUntilReclamation(t *testing.T) {
	srv := &Server{clientDBs: clientdb.NewDBs(t.TempDir())}
	ps := NewPubSub(srv)
	sess := &daggerSession{
		sessionID:       "session",
		telemetryPubSub: ps,
		clientRuntimes:  make(map[string]*clientRuntime),
	}
	sess.state.Store(sessionStateInitialized)
	const destinations = 33
	clients := make([]*clientRuntime, destinations)
	stores := make([]*clientdb.DB, destinations)
	for i := range clients {
		client := &clientRuntime{
			clientRecord: &clientRecord{clientID: fmt.Sprintf("client-%d", i), daggerSession: sess},
			state:        clientStateInitialized,
		}
		clients[i] = client
		sess.clientRuntimes[client.clientID] = client
		require.NoError(t, client.retainTelemetryDB(t.Context()))
		t.Cleanup(func() { require.NoError(t, client.closeTelemetryDB()) })
		stores[i] = client.telemetryDB
	}
	installTestClientRecords(sess)

	// No subscriptions or test probes retain a reference between exports.
	// Replaying this history on each export used to thrash the 32-store LRU.
	history := make([]sdklog.Record, 2000)
	for i := range history {
		history[i].SetBody(log.StringValue(fmt.Sprintf("history-%d", i)))
	}
	for _, client := range clients {
		require.NoError(t, ps.Logs(client.clientID).Export(t.Context(), history))
	}
	for range 3 {
		for i, client := range clients {
			require.NoError(t, ps.Logs(client.clientID).Export(t.Context(), history[:1]))
			probe, err := client.TelemetryDB(t.Context())
			require.NoError(t, err)
			require.Same(t, stores[i], probe, "live exports must reuse recovered indexes")
			require.NoError(t, probe.Close())
		}
	}
	require.Equal(t, clientdb.OpenStats{Stores: destinations, Streams: 3 * destinations, Refs: destinations}, srv.clientDBs.OpenStats())

	// An active reader can outlive its runtime. All other stores close as soon
	// as their runtime is reclaimed, regardless of the number of destinations.
	reader, err := clients[0].TelemetryDB(t.Context())
	require.NoError(t, err)
	for _, client := range clients {
		sess.scopeMu.Lock()
		reclamation := sess.maybeBeginClientRuntimeReclamationLocked(client)
		sess.scopeMu.Unlock()
		sess.finishClientRuntimeReclamation(reclamation)
		require.Nil(t, client.telemetryDB)
	}
	require.Empty(t, sess.clientRuntimes)
	require.Len(t, sess.clientRecords, destinations)
	require.Equal(t, clientdb.OpenStats{Stores: 1, Streams: 3, Refs: 1}, srv.clientDBs.OpenStats())
	rows, err := reader.SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{Limit: 3000})
	require.NoError(t, err)
	require.Len(t, rows, 2003)
	require.NoError(t, reader.Close())
	require.Equal(t, clientdb.OpenStats{}, srv.clientDBs.OpenStats())

	// A late export reopens persisted history temporarily and does not recreate
	// either runtime ownership or an idle cache entry.
	require.NoError(t, ps.Logs(clients[0].clientID).Export(t.Context(), history[:1]))
	require.Equal(t, clientdb.OpenStats{}, srv.clientDBs.OpenStats())
	historical, err := clients[0].clientRecord.TelemetryDB(t.Context())
	require.NoError(t, err)
	require.NotSame(t, stores[0], historical)
	rows, err = historical.SelectLogsSince(t.Context(), clientdb.SelectLogsSinceParams{ID: 2000, Limit: 10})
	require.NoError(t, err)
	require.Len(t, rows, 4)
	require.NoError(t, historical.Close())
	require.Empty(t, sess.clientRuntimes)
	require.Equal(t, clientdb.OpenStats{}, srv.clientDBs.OpenStats())
	require.NoError(t, srv.clientDBs.Close())
}

// A client's store closes when its runtime is reclaimed, but telemetry for the
// client can still arrive afterwards: the session's shutdown flush exports
// whatever its processors still hold. Appending that must not replay the
// store's history, which for a long-running client took longer than the 10s
// the CLI gives shutdown. A context whose deadline has already passed stands
// in for the shutdown's: an append that reads no history never consults it.
func TestLateExportToReclaimedClientDoesNotReplayStore(t *testing.T) {
	srv := &Server{clientDBs: clientdb.NewDBs(t.TempDir())}
	t.Cleanup(func() { require.NoError(t, srv.clientDBs.Close()) })
	ps := NewPubSub(srv)
	sess := &daggerSession{
		sessionID:       "session",
		telemetryPubSub: ps,
		clientRuntimes:  make(map[string]*clientRuntime),
	}
	sess.state.Store(sessionStateInitialized)
	client := &clientRuntime{
		clientRecord: &clientRecord{clientID: "client", daggerSession: sess},
		state:        clientStateInitialized,
	}
	sess.clientRuntimes[client.clientID] = client
	require.NoError(t, client.retainTelemetryDB(t.Context()))
	installTestClientRecords(sess)

	const traceID = "00000000000000000000000000000001"
	spanRow := func(i int) clientdb.Span {
		return clientdb.Span{TraceID: traceID, SpanID: fmt.Sprintf("%016x", i), Name: fmt.Sprintf("span-%d", i)}
	}
	const history = 2000
	spans := make([]clientdb.Span, history)
	logs := make([]sdklog.Record, history)
	for i := range history {
		spans[i] = spanRow(i + 1)
		logs[i].SetBody(log.StringValue(fmt.Sprintf("history-%d", i)))
	}
	require.NoError(t, ps.appendSpans(t.Context(), client.clientID, spans))
	require.NoError(t, ps.Logs(client.clientID).Export(t.Context(), logs))

	sess.scopeMu.Lock()
	reclamation := sess.maybeBeginClientRuntimeReclamationLocked(client)
	sess.scopeMu.Unlock()
	sess.finishClientRuntimeReclamation(reclamation)
	require.Equal(t, clientdb.OpenStats{}, srv.clientDBs.OpenStats())

	expired, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	late := spanRow(history + 1)
	lateSpanID, err := trace.SpanIDFromHex(late.SpanID)
	require.NoError(t, err)
	var lateLog sdklog.Record
	lateLog.SetBody(log.StringValue("late"))
	lateLog.SetSpanID(lateSpanID)
	require.NoError(t, ps.appendSpans(expired, client.clientID, []clientdb.Span{late}))
	require.NoError(t, ps.Logs(client.clientID).Export(expired, []sdklog.Record{lateLog}))
	require.Equal(t, clientdb.OpenStats{}, srv.clientDBs.OpenStats())

	// A reader sees the history and the late rows, and finds them through the
	// store's indexes.
	reader, err := client.clientRecord.TelemetryDB(t.Context())
	require.NoError(t, err)
	defer reader.Close()
	require.True(t, reader.HasSpan(spanRow(1).SpanID))
	require.True(t, reader.HasSpan(late.SpanID))
	require.Len(t, reader.SpanIDs(), history+1)
	row, err := reader.SelectSpan(t.Context(), clientdb.SelectSpanParams{TraceID: traceID, SpanID: late.SpanID})
	require.NoError(t, err)
	require.Equal(t, int64(history+1), row.ID)
	require.Equal(t, late.Name, row.Name)
	lateLogs, err := reader.SelectLogsForSpans(t.Context(), map[string]struct{}{late.SpanID: {}}, 0)
	require.NoError(t, err)
	require.Len(t, lateLogs, 1)
	require.Equal(t, int64(history+1), lateLogs[0].ID)
}

// TestRetainedStoreIndexesOnlyForIndexedReader: a client holds its store open
// without indexes, so a session whose readers only stream it builds none. The
// first reader that needs them builds them over everything written so far.
func TestRetainedStoreIndexesOnlyForIndexedReader(t *testing.T) {
	srv := &Server{clientDBs: clientdb.NewDBs(t.TempDir())}
	t.Cleanup(func() { require.NoError(t, srv.clientDBs.Close()) })
	ps := NewPubSub(srv)
	sess := &daggerSession{
		sessionID:       "session",
		telemetryPubSub: ps,
		clientRuntimes:  make(map[string]*clientRuntime),
	}
	client := &clientRuntime{
		clientRecord: &clientRecord{clientID: "client", daggerSession: sess},
		state:        clientStateInitialized,
	}
	sess.clientRuntimes[client.clientID] = client
	installTestClientRecords(sess)
	require.NoError(t, client.retainTelemetryDB(t.Context()))
	defer func() { require.NoError(t, client.closeTelemetryDB()) }()

	const traceID = "00000000000000000000000000000001"
	parent := clientdb.Span{TraceID: traceID, SpanID: "0000000000000001", Name: "parent"}
	child := clientdb.Span{TraceID: traceID, SpanID: "0000000000000002", Name: "child",
		ParentSpanID: sql.NullString{String: parent.SpanID, Valid: true}}
	require.NoError(t, ps.appendSpans(t.Context(), client.clientID, []clientdb.Span{parent, child}))

	// Streaming reads rows in order through the retained, unindexed store.
	streamed, err := client.telemetryDB.Read().SelectSpansSince(t.Context(), clientdb.SelectSpansSinceParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, streamed, 2)
	require.Empty(t, client.telemetryDB.SpanIDs(), "holding and streaming the store builds no index")

	reader, err := client.clientRecord.TelemetryDB(t.Context())
	require.NoError(t, err)
	defer reader.Close()
	require.Same(t, client.telemetryDB, reader, "the indexed reader shares the retained store")
	require.Equal(t, map[string]struct{}{child.SpanID: {}}, reader.ChildSpanIDs(parent.SpanID))

	late := clientdb.Span{TraceID: traceID, SpanID: "0000000000000003", Name: "late",
		ParentSpanID: sql.NullString{String: parent.SpanID, Valid: true}}
	require.NoError(t, ps.appendSpans(t.Context(), client.clientID, []clientdb.Span{late}))
	require.True(t, reader.HasSpan(late.SpanID), "once built, the indexes follow new rows")
}
