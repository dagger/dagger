package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The status names the engine's clients by client ID and claim. A client is
// open while any of its sessions is live, and ended once its last one has,
// for endedClientRetention or until a session with its ID starts again.
// Without the remote cache, its state is settled whenever no session is live.
func TestEngineStatusClients(t *testing.T) {
	t.Parallel()
	now := time.Unix(1790000000, 0).UTC()
	l := &sessionLedger{now: func() time.Time { return now }}
	require.Equal(t, EngineStatus{
		EngineID:    "engine-1",
		Open:        []StatusClient{},
		Ended:       []StatusEndedClient{},
		RemoteCache: StatusRemoteCache{State: RemoteCacheSettled},
	}, l.status("engine-1"))

	l.sessionStarted("a", "claim-a")
	l.sessionStarted("a", "claim-a")
	l.sessionStarted("b", "")
	status := l.status("engine-1")
	require.Equal(t, []StatusClient{{ClientID: "a", ClaimID: "claim-a"}, {ClientID: "b"}}, status.Open)
	require.Empty(t, status.Ended)
	require.Equal(t, RemoteCacheBusy, status.RemoteCache.State)

	l.sessionEnded("a", "claim-a")
	l.sessionEnded("b", "")
	status = l.status("engine-1")
	require.Equal(t, []StatusClient{{ClientID: "a", ClaimID: "claim-a"}}, status.Open, "a has a live session left")
	require.Equal(t, []StatusEndedClient{{ClientID: "b", EndedAt: now}}, status.Ended)

	bEnded := now
	now = now.Add(time.Second)
	l.sessionEnded("a", "claim-a")
	status = l.status("engine-1")
	require.Empty(t, status.Open)
	require.Equal(t, []StatusEndedClient{{ClientID: "a", ClaimID: "claim-a", EndedAt: now}, {ClientID: "b", EndedAt: bEnded}}, status.Ended)
	require.Equal(t, RemoteCacheSettled, status.RemoteCache.State)

	now = bEnded.Add(endedClientRetention)
	require.Equal(t, []StatusEndedClient{{ClientID: "a", ClaimID: "claim-a", EndedAt: bEnded.Add(time.Second)}}, l.status("engine-1").Ended, "b is no longer reported")

	l.sessionStarted("a", "claim-a2")
	status = l.status("engine-1")
	require.Equal(t, []StatusClient{{ClientID: "a", ClaimID: "claim-a2"}}, status.Open)
	require.Empty(t, status.Ended, "a session with a's ID started again")
}

// GET /v1/status answers the status as JSON, with its documented keys, and
// needs no session. Any other method is refused.
func TestServeStatus(t *testing.T) {
	t.Parallel()
	srv := &Server{engineName: "engine-1"}
	srv.sessionLedger.sessionStarted("client-1", "claim-1")

	w := httptest.NewRecorder()
	srv.ServeStatus(w, httptest.NewRequest(http.MethodGet, StatusPath, nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, map[string]any{
		"engineID":    "engine-1",
		"open":        []any{map[string]any{"clientID": "client-1", "claimID": "claim-1"}},
		"ended":       []any{},
		"remoteCache": map[string]any{"state": "busy", "idlePeriod": float64(0)},
	}, got)

	w = httptest.NewRecorder()
	srv.ServeStatus(w, httptest.NewRequest(http.MethodPost, StatusPath, nil))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
