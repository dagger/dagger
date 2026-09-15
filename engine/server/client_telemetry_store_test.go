package server

import (
	"fmt"
	"testing"

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
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
