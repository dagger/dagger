package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/stretchr/testify/require"
	logapi "go.opentelemetry.io/otel/log"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
)

// plainSessionFixture is a session that recorded telemetry in its primary
// trace but no agent state, so it never registered an archive of its own.
func plainSessionFixture(t *testing.T, srv *Server, sessionID, clientID string, record bool) *daggerSession {
	t.Helper()
	sess := &daggerSession{sessionID: sessionID, mainClientCallerID: clientID, clientRecords: map[string]*clientRecord{}}
	sess.telemetryPubSub = NewPubSub(srv)
	sess.clientRecords[clientID] = &clientRecord{daggerSession: sess, clientID: clientID, clientMetadata: &engine.ClientMetadata{
		ClientSecretToken: "secret", PrimaryTraceID: archiveTestTrace,
	}}
	if record {
		db, err := srv.clientDBs.Open(t.Context(), clientID)
		require.NoError(t, err)
		rec := scopedLogRecord(t, "test", logapi.StringValue("PLAIN-SESSION-OUTPUT"))
		row, err := logRecordRow(&rec)
		require.NoError(t, err)
		_, err = db.AppendLogs([]clientdb.Log{row})
		require.NoError(t, err)
		require.NoError(t, db.Close())
	}
	return sess
}

func telemetryArchiveServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	dbs := clientdb.NewDBs(filepath.Join(root, "stores"))
	manager, err := archive.NewManager(archive.Config{Root: filepath.Join(root, "archives"), RemoveStore: dbs.Remove, StoreSize: dbs.StoreSize})
	require.NoError(t, err)
	return &Server{clientDBs: dbs, archives: manager}
}

// TestArchiveRetainsPlainSession: a session without agents is retained by its
// primary trace and sealed with an empty roster, readable like any archive,
// but never listed or bootstrapped for agent restore.
func TestArchiveRetainsPlainSession(t *testing.T) {
	srv := telemetryArchiveServer(t)
	sess := plainSessionFixture(t, srv, "session", "main", true)
	require.NoError(t, srv.finalizeSessionArchive(t.Context(), sess, nil))
	m, err := srv.archives.Manifest(archiveTestTrace)
	require.NoError(t, err)
	require.Equal(t, archive.StateClosed, m.State, "failure: %s", m.Failure)
	require.True(t, m.TelemetryOnly)
	require.Equal(t, "main", m.MainClientID)
	require.Positive(t, m.SizeBytes, "retained telemetry counts toward the archive quota")

	// Read it from another session, as `dagger trace` would.
	reader := plainSessionFixture(t, srv, "reader", "reader", false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := srv.serveArchiveHTTP(w, r, reader.clientRecords["reader"]); err != nil {
			t.Errorf("archive HTTP: %v", err)
		}
	}))
	defer server.Close()
	client, err := archive.NewClientWithURL(server.Client(), server.URL)
	require.NoError(t, err)
	inspected, err := client.Inspect(t.Context(), archiveTestTrace)
	require.NoError(t, err)
	require.True(t, inspected.TelemetryOnly)
	var output strings.Builder
	_, err = client.Logs(t.Context(), archiveTestTrace, archive.StreamOptions{HighWater: inspected.HighWater.Logs}, func(_ int64, batch *collogspb.ExportLogsServiceRequest) error {
		for _, resource := range batch.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				for _, rec := range scope.LogRecords {
					output.WriteString(rec.GetBody().GetStringValue())
				}
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, "PLAIN-SESSION-OUTPUT", output.String())

	listed, err := client.ListAll(t.Context(), archive.ListOptions{})
	require.NoError(t, err)
	require.Empty(t, listed, "a plain session is not an agent session to resume")
	_, err = client.Bootstrap(t.Context(), archiveTestTrace, nil)
	require.True(t, archive.IsCleanMiss(err), "agent restore of a plain session must be a clean miss, got %v", err)
}

// TestArchivePlainSessionKeepsOwnedTrace: a plain session neither displaces an
// archive another session owns nor registers one when it recorded nothing.
func TestArchivePlainSessionKeepsOwnedTrace(t *testing.T) {
	srv, agentSess, _, _ := archiveFixture(t)
	plain := plainSessionFixture(t, srv, "plain", "plain-main", true)
	require.NoError(t, srv.finalizeSessionArchive(t.Context(), plain, nil))
	require.NoError(t, srv.finalizeSessionArchive(t.Context(), agentSess, nil))
	m, err := srv.archives.Manifest(archiveTestTrace)
	require.NoError(t, err)
	require.Equal(t, archive.StateClosed, m.State, "failure: %s", m.Failure)
	require.False(t, m.TelemetryOnly)
	require.Equal(t, "main", m.MainClientID)

	empty := telemetryArchiveServer(t)
	require.NoError(t, empty.finalizeSessionArchive(t.Context(), plainSessionFixture(t, empty, "idle", "idle", false), nil))
	_, err = empty.archives.Manifest(archiveTestTrace)
	require.Error(t, err, "a session that recorded nothing is not retained")
}

// TestArchiveAgentSessionSupersedesPlainSession: an agent session sharing a
// trace with an ended plain session still gets its archive.
func TestArchiveAgentSessionSupersedesPlainSession(t *testing.T) {
	srv := telemetryArchiveServer(t)
	plain := plainSessionFixture(t, srv, "plain", "plain-main", true)
	require.NoError(t, srv.finalizeSessionArchive(t.Context(), plain, nil))
	agent := &daggerSession{sessionID: "session", mainClientCallerID: "main", clientRecords: map[string]*clientRecord{}}
	agent.telemetryPubSub = NewPubSub(srv)
	require.NoError(t, agent.ensureArchive(archiveTestTrace))
	m, err := srv.archives.Manifest(archiveTestTrace)
	require.NoError(t, err)
	require.False(t, m.TelemetryOnly)
	require.Equal(t, "main", m.MainClientID)
	require.Equal(t, archive.StateActive, m.State)
}
