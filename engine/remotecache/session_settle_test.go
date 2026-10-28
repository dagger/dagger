package remotecache

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// servedSession serves a session to adapter, past the service's welcome.
// The test's end cancels it and waits for serve to return before the
// session closes.
func servedSession(t *testing.T, adapter *fakeAdapter) (*session, *recordingConn, protocol.Hello) {
	t.Helper()
	s, conn := testSession(t, adapter)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_, _ = s.serve()
	}()
	t.Cleanup(func() {
		s.cancel(errors.New("done"))
		<-served
	})
	hello := conn.next(t)
	require.Equal(t, protocol.TypeHello, hello.Type)
	s.dispatch(protocol.Envelope{ID: 1, Re: hello.ID, Type: protocol.TypeWelcome, Body: json.RawMessage(`{}`)})
	return s, conn, decode[protocol.Hello](t, hello)
}

func reply(re uint64, typ protocol.Type, body any) protocol.Envelope {
	raw, _ := json.Marshal(body)
	return protocol.Envelope{ID: re + 1000, Re: re, Type: typ, Body: raw}
}

func noSettled(t *testing.T, adapter *fakeAdapter) {
	t.Helper()
	select {
	case call := <-adapter.settled:
		t.Fatalf("unexpected Settled for period %d", call.period)
	case <-time.After(50 * time.Millisecond):
	}
}

// The hello names the engine's current idle period, and whether it connects
// settled for it with no session live.
func TestHelloCarriesTheIdlePeriod(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		state SettleState
		want  protocol.Hello
	}{
		{SettleState{Period: 3, Settled: true}, protocol.Hello{Period: 3, Settled: true}},
		{SettleState{Period: 3, Live: true, Settled: true}, protocol.Hello{Period: 3}},
		{SettleState{Period: 3, Sessions: []protocol.SettleSession{{ID: "sess-1"}}}, protocol.Hello{Period: 3}},
	} {
		adapter := newFakeAdapter()
		adapter.setSettle(tc.state)
		_, _, hello := servedSession(t, adapter)
		require.Equal(t, tc.want.Period, hello.Period)
		require.Equal(t, tc.want.Settled, hello.Settled)
	}
}

// Once no session is live, the engine asks the service to settle its idle
// period, listing every ended session the service hasn't covered, with
// whether each sent its completion marker. The service's Settled covers them.
func TestSettleListsTheEndedSessions(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	adapter.setSettle(SettleState{Period: 1, Live: true})
	s, conn, _ := servedSession(t, adapter)
	conn.none(t)

	sessions := []protocol.SettleSession{{ID: "sess-1", Marker: true}, {ID: "sess-2"}}
	adapter.setSettle(SettleState{Period: 1, Sessions: sessions})
	settle := conn.next(t)
	require.Equal(t, protocol.TypeSettle, settle.Type)
	require.Equal(t, protocol.Settle{Period: 1, Sessions: sessions}, decode[protocol.Settle](t, settle))
	s.dispatch(reply(settle.ID, protocol.TypeSettled, protocol.Settled{}))
	select {
	case call := <-adapter.settled:
		require.Equal(t, settledCall{period: 1, sessions: sessions}, call)
	case <-time.After(5 * time.Second):
		t.Fatal("no Settled")
	}
	conn.none(t)
}

// A session that starts on an engine with none live opens a new idle period,
// which the engine tells the service at once.
func TestOpenTellsTheServiceOfANewPeriod(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	adapter.setSettle(SettleState{Period: 1, Settled: true})
	s, conn, _ := servedSession(t, adapter)
	conn.none(t)

	adapter.setSettle(SettleState{Period: 2, Live: true})
	open := conn.next(t)
	require.Equal(t, protocol.TypeOpen, open.Type)
	require.Equal(t, protocol.Open{Period: 2}, decode[protocol.Open](t, open))
	s.dispatch(reply(open.ID, protocol.TypeOpened, protocol.Opened{}))
	conn.none(t)
}

// A settle still waiting when a session starts is abandoned: a late reply to
// it settles nothing, and once the new session has ended the engine asks
// again, still listing the earlier session.
func TestSettleIsAbandonedWhenASessionStarts(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	first := protocol.SettleSession{ID: "sess-1", Marker: true}
	adapter.setSettle(SettleState{Period: 1, Sessions: []protocol.SettleSession{first}})
	s, conn, _ := servedSession(t, adapter)
	stale := conn.next(t)
	require.Equal(t, protocol.TypeSettle, stale.Type)

	adapter.setSettle(SettleState{Period: 2, Live: true, Sessions: []protocol.SettleSession{first}})
	open := conn.next(t)
	require.Equal(t, protocol.Open{Period: 2}, decode[protocol.Open](t, open))
	s.dispatch(reply(open.ID, protocol.TypeOpened, protocol.Opened{}))
	s.dispatch(reply(stale.ID, protocol.TypeSettled, protocol.Settled{}))
	noSettled(t, adapter)

	second := protocol.SettleSession{ID: "sess-2"}
	adapter.setSettle(SettleState{Period: 2, Sessions: []protocol.SettleSession{first, second}})
	settle := conn.next(t)
	require.Equal(t, protocol.Settle{Period: 2, Sessions: []protocol.SettleSession{first, second}}, decode[protocol.Settle](t, settle))
}

// A settle the service finds stale settles nothing, and the engine doesn't
// ask again until its state changes.
func TestStaleSettleSettlesNothing(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	adapter.setSettle(SettleState{Period: 1, Sessions: []protocol.SettleSession{{ID: "sess-1"}}})
	s, conn, _ := servedSession(t, adapter)
	settle := conn.next(t)
	require.Equal(t, protocol.TypeSettle, settle.Type)
	s.dispatch(reply(settle.ID, protocol.TypeError, protocol.Error{Message: protocol.StaleSettle}))
	noSettled(t, adapter)
	conn.none(t)
}

// An engine that gave up settling asks nothing more.
func TestTimedOutEngineAsksNothing(t *testing.T) {
	t.Parallel()
	adapter := newFakeAdapter()
	adapter.setSettle(SettleState{Period: 1, Sessions: []protocol.SettleSession{{ID: "sess-1"}}})
	_, conn, _ := servedSession(t, adapter)
	require.Equal(t, protocol.TypeSettle, conn.next(t).Type)
	adapter.setSettle(SettleState{Period: 1, Sessions: []protocol.SettleSession{{ID: "sess-1"}}, TimedOut: true})
	conn.none(t)
}
