package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
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

	l.sessionEnded("sess-a1", "a", "claim-a", true)
	l.sessionEnded("sess-b", "b", "", true)
	status = l.status("engine-1")
	require.Equal(t, []StatusClient{{ClientID: "a", ClaimID: "claim-a"}}, status.Open, "a has a live session left")
	require.Equal(t, []StatusEndedClient{{ClientID: "b", EndedAt: now}}, status.Ended)

	bEnded := now
	now = now.Add(time.Second)
	l.sessionEnded("sess-a2", "a", "claim-a", true)
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

// remoteSettleTest is a ledger with the remote cache attached, its exports in
// progress set by the test, its pauses and resumes counted, and a short bound.
type remoteSettleTest struct {
	ledger          sessionLedger
	mu              sync.Mutex
	exports         int
	pauses, resumes int
}

func newRemoteSettleTest(bound time.Duration) *remoteSettleTest {
	rt := &remoteSettleTest{}
	rt.ledger.attachRemote(func() int {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		return rt.exports
	}, func() {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		rt.pauses++
	}, func() {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		rt.resumes++
	})
	rt.ledger.remote.bound = bound
	return rt
}

func (rt *remoteSettleTest) counts() (pauses, resumes int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.pauses, rt.resumes
}

func (rt *remoteSettleTest) remoteCache() StatusRemoteCache {
	return rt.ledger.status("engine-1").RemoteCache
}

// With the remote cache, a session on an engine with none live opens an idle
// period. Each ended session waits, with whether it sent its completion
// marker, for the service's Settled for that period; the engine is settled
// once that covers them all and no export is in progress.
func TestRemoteCacheSettles(t *testing.T) {
	t.Parallel()
	rt := newRemoteSettleTest(time.Hour)
	require.Equal(t, StatusRemoteCache{State: RemoteCacheSettled}, rt.remoteCache(), "nothing to settle yet")

	rt.ledger.sessionStarted("a", "claim-a")
	rt.ledger.sessionStarted("b", "claim-b")
	require.Equal(t, StatusRemoteCache{State: RemoteCacheBusy, IdlePeriod: 1}, rt.remoteCache())
	rt.ledger.sessionEnded("sess-a", "a", "claim-a", true)
	state, _ := rt.ledger.settleState()
	require.True(t, state.Live, "b is live: nothing to settle yet")
	rt.ledger.sessionEnded("sess-b", "b", "claim-b", false)
	sessions := []protocol.SettleSession{{ID: "sess-a", Marker: true}, {ID: "sess-b"}}
	state, changed := rt.ledger.settleState()
	require.Equal(t, remotecache.SettleState{Period: 1, Sessions: sessions}, state)
	require.Equal(t, StatusRemoteCache{State: RemoteCacheBusy, IdlePeriod: 1}, rt.remoteCache(), "unacknowledged sessions")

	rt.ledger.settledFor(1, sessions)
	select {
	case <-changed:
	default:
		t.Fatal("a Settled changes the settle state")
	}
	state, _ = rt.ledger.settleState()
	require.Equal(t, remotecache.SettleState{Period: 1, Sessions: []protocol.SettleSession{}, Settled: true}, state)
	require.Equal(t, StatusRemoteCache{State: RemoteCacheSettled, IdlePeriod: 1}, rt.remoteCache())

	rt.mu.Lock()
	rt.exports = 1
	rt.mu.Unlock()
	require.Equal(t, RemoteCacheBusy, rt.remoteCache().State, "an export is in progress")
	rt.mu.Lock()
	rt.exports = 0
	rt.mu.Unlock()

	rt.ledger.sessionStarted("c", "")
	require.Equal(t, StatusRemoteCache{State: RemoteCacheBusy, IdlePeriod: 2}, rt.remoteCache())
	rt.ledger.settledFor(1, nil)
	rt.ledger.sessionEnded("sess-c", "c", "", true)
	state, _ = rt.ledger.settleState()
	require.False(t, state.Settled, "a Settled for an earlier period settles nothing")
	require.Equal(t, []protocol.SettleSession{{ID: "sess-c", Marker: true}}, state.Sessions)
}

// Once the bound passes after the last session ended without the engine
// settling, it gives up its remote-cache work: it reports timed out, pauses,
// and a Settled after that settles nothing. A session that starts again
// resumes it, opens a new period, and keeps the sessions still unacknowledged.
func TestRemoteCacheTimesOut(t *testing.T) {
	t.Parallel()
	rt := newRemoteSettleTest(20 * time.Millisecond)
	rt.ledger.sessionStarted("a", "")
	rt.ledger.sessionEnded("sess-a", "a", "", true)
	require.Eventually(t, func() bool { return rt.remoteCache().State == RemoteCacheTimedOut }, 5*time.Second, 5*time.Millisecond)
	pauses, resumes := rt.counts()
	require.Equal(t, 1, pauses)
	require.Zero(t, resumes)
	rt.ledger.settledFor(1, []protocol.SettleSession{{ID: "sess-a", Marker: true}})
	require.Equal(t, RemoteCacheTimedOut, rt.remoteCache().State, "timed out stays until a session starts")

	rt.ledger.sessionStarted("b", "")
	pauses, resumes = rt.counts()
	require.Equal(t, 1, pauses)
	require.Equal(t, 1, resumes)
	require.Equal(t, StatusRemoteCache{State: RemoteCacheBusy, IdlePeriod: 2}, rt.remoteCache())
	state, _ := rt.ledger.settleState()
	require.Equal(t, []protocol.SettleSession{{ID: "sess-a", Marker: true}}, state.Sessions)
}

// A settled engine doesn't time out.
func TestSettledEngineDoesNotTimeOut(t *testing.T) {
	t.Parallel()
	rt := newRemoteSettleTest(20 * time.Millisecond)
	rt.ledger.sessionStarted("a", "")
	rt.ledger.sessionEnded("sess-a", "a", "", false)
	rt.ledger.settledFor(1, []protocol.SettleSession{{ID: "sess-a"}})
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, RemoteCacheSettled, rt.remoteCache().State)
	pauses, _ := rt.counts()
	require.Zero(t, pauses)
}

// A session that starts while the engine is giving up its remote-cache work
// takes it up again after the pause, not before it.
func TestSessionStartingAsTheEngineTimesOutResumes(t *testing.T) {
	t.Parallel()
	var l sessionLedger
	var p exportPause
	pausing, release, paused := make(chan struct{}), make(chan struct{}), make(chan struct{})
	l.attachRemote(func() int { return 0 }, func() {
		close(pausing)
		<-release
		p.pause()
		close(paused)
	}, p.resume)
	l.remote.bound = time.Millisecond
	l.sessionStarted("a", "")
	l.sessionEnded("sess-a", "a", "", true)
	<-pausing

	started := make(chan struct{})
	go func() {
		l.sessionStarted("b", "")
		close(started)
	}()
	select {
	case <-started:
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-paused
	<-started
	_, done, ok := p.begin(context.Background())
	defer done()
	require.True(t, ok, "the engine takes exports while a session is live")
}

// Pausing cancels the exports in progress and refuses new ones, until a
// resume.
func TestExportPause(t *testing.T) {
	t.Parallel()
	var p exportPause
	ctx, release, ok := p.begin(context.Background())
	require.True(t, ok)
	defer release()
	p.pause()
	require.ErrorIs(t, context.Cause(ctx), errRemoteCacheTimedOut)
	_, _, ok = p.begin(context.Background())
	require.False(t, ok)
	p.resume()
	_, release2, ok := p.begin(context.Background())
	require.True(t, ok)
	release2()
}

// An adapter whose engine gave up its remote-cache work answers an export at
// once, with every root refused.
func TestPausedAdapterRefusesExports(t *testing.T) {
	t.Parallel()
	var a RemoteCacheAdapter
	a.paused.pause()
	var got remotecache.Export
	require.NoError(t, a.Export(context.Background(), protocol.Export{Roots: []uint64{12, 13}}, func(_ context.Context, exp remotecache.Export) error {
		got = exp
		return nil
	}))
	require.Nil(t, got.Bundle)
	require.Len(t, got.Skipped, 2)
	for i, number := range []uint64{12, 13} {
		require.Equal(t, number, got.Skipped[i].Number)
		require.Equal(t, protocol.SkipRefused, got.Skipped[i].Reason)
	}
}
