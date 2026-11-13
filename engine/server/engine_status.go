package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// StatusPath is where the engine's main listener serves its status, outside
// any session: GET answers EngineStatus.
const StatusPath = "/v1/status"

// endedClientRetention is how long the status keeps reporting a client
// whose sessions have all ended, unless a session with its ID starts again.
const endedClientRetention = 2 * time.Minute

// remoteCacheSettleBound is how long, after its last session ends, the
// engine waits for the remote cache service to settle before it gives up
// its remote-cache work: it cancels its exports in progress and refuses new
// ones until a session starts again. It matches the drain's export window;
// it is a sensible value, not a measured one.
const remoteCacheSettleBound = 20 * time.Second

// Remote cache states the status reports (EngineStatus.RemoteCache).
const (
	// RemoteCacheBusy: a session is live, or the remote cache still has work
	// with the engine.
	RemoteCacheBusy = "busy"
	// RemoteCacheSettled: no session is live, and the remote cache has none.
	RemoteCacheSettled = "settled"
	// RemoteCacheTimedOut: the remote cache didn't settle within its bound
	// after the last session ended, and the engine gave up its remote-cache
	// work until a session starts again.
	RemoteCacheTimedOut = "timedOut"
)

// EngineStatus is what the engine says of itself outside any session, for
// Dagger Cloud to stop it once it's done: its clients, by client ID and the
// Dagger Cloud claim they connected under, and its remote cache's state.
type EngineStatus struct {
	EngineID string `json:"engineID"`
	// Open are the clients with a live session.
	Open []StatusClient `json:"open"`
	// Ended are the clients with no live session left, for
	// endedClientRetention after their last one ended.
	Ended       []StatusEndedClient `json:"ended"`
	RemoteCache StatusRemoteCache   `json:"remoteCache"`
}

type StatusClient struct {
	ClientID string `json:"clientID"`
	ClaimID  string `json:"claimID"`
}

type StatusEndedClient struct {
	ClientID string `json:"clientID"`
	ClaimID  string `json:"claimID"`
	// EndedAt is when the client's last session ended, by the engine's clock.
	EndedAt time.Time `json:"endedAt"`
}

type StatusRemoteCache struct {
	State string `json:"state"`
	// IdlePeriod numbers the engine's idle periods with the remote cache, as
	// it tells the service; 0 without the remote cache.
	IdlePeriod uint64 `json:"idlePeriod"`
}

// clientClaim is a client ID and the claim its session connected under.
type clientClaim struct {
	clientID, claimID string
}

// sessionLedger counts the engine's live root sessions by client and claim,
// and keeps the clients whose sessions have all ended. Several sessions can
// share a client ID, so a client ends only with its last live session. The
// zero value is ready to use.
type sessionLedger struct {
	mu sync.Mutex
	// now is the clock; nil is time.Now.
	now      func() time.Time
	live     map[clientClaim]int
	sessions int
	// tearingDown are the live sessions whose teardown has begun
	// (sessionTearingDown): their clients are gone, and they only stop
	// their services and flush before they end.
	tearingDown map[string]struct{}
	ended       map[clientClaim]time.Time
	// claims are the sessions that started under a claim, by session ID,
	// with when each started, which the remote cache's settle state reports
	// (settleState): live ones, and, with the remote cache, ended ones until
	// a Settled covers them, so that a session that ends before its claim is
	// sent is still sent before its settle.
	claims map[string]sessionClaim
	// remote is the remote cache's settle state, while an integration is
	// attached (attachRemote).
	remote *remoteSettle
	// changed is closed, and replaced, when the remote cache's settle state
	// changes (settleState).
	changed chan struct{}
}

// remoteSettle is the engine's side of settling with the remote cache
// service: the idle periods it numbers, the ended sessions no Settled has
// covered yet, and its bound after the last session's end.
type remoteSettle struct {
	// exports counts the exports in progress; pause gives up the remote
	// cache's work once the bound passes, and resume takes it up again when
	// a session starts.
	exports       func() int
	pause, resume func()
	bound         time.Duration
	// period is the current idle period: it starts when a session starts on
	// an engine with none live, and the sessions then live end it.
	period  uint64
	unacked []protocol.SettleSession
	// settled is set once a Settled covered period, and timedOut once the
	// bound passed without the engine settling, until a session starts.
	settled, timedOut bool
	timer             *time.Timer
}

// sessionClaim is a session's claim, and when the session started.
type sessionClaim struct {
	claim   string
	started time.Time
}

func (l *sessionLedger) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// sessionStarted counts a live root session sessionID of clientID under
// claimID. With the remote cache, a session on an engine with none live
// starts a new idle period, and ends a timed-out pause, and a session with a
// claim is reported to the service (protocol.Claims).
func (l *sessionLedger) sessionStarted(sessionID, clientID, claimID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.live == nil {
		l.live = map[clientClaim]int{}
	}
	l.live[clientClaim{clientID, claimID}]++
	l.sessions++
	if claimID != "" {
		if l.claims == nil {
			l.claims = map[string]sessionClaim{}
		}
		l.claims[sessionID] = sessionClaim{claim: claimID, started: l.clock()}
		if l.remote != nil {
			l.notifyLocked()
		}
	}
	for ended := range l.ended {
		if ended.clientID == clientID {
			delete(l.ended, ended)
		}
	}
	if r := l.remote; r != nil && l.sessions == 1 {
		r.period++
		r.settled = false
		if r.timer != nil {
			r.timer.Stop()
			r.timer = nil
		}
		if r.timedOut {
			r.timedOut = false
			r.resume()
		}
		l.notifyLocked()
	}
}

// sessionEnded counts down a root session sessionStarted counted, and
// records its client as ended once no live session of it remains. With the
// remote cache, the session waits for the service's Settled, with whether it
// sent its completion marker, and the last live session's end starts the
// bound.
func (l *sessionLedger) sessionEnded(sessionID, clientID, claimID string, marker bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := clientClaim{clientID, claimID}
	delete(l.tearingDown, sessionID)
	if l.live[key] == 0 {
		return
	}
	l.sessions--
	if l.remote == nil {
		delete(l.claims, sessionID)
	}
	if r := l.remote; r != nil {
		r.unacked = append(r.unacked, protocol.SettleSession{ID: sessionID, Marker: marker})
		if l.sessions == 0 {
			period := r.period
			r.timer = time.AfterFunc(r.bound, func() { l.settleTimedOut(r, period) })
		}
		l.notifyLocked()
	}
	l.live[key]--
	if l.live[key] > 0 {
		return
	}
	delete(l.live, key)
	if l.ended == nil {
		l.ended = map[clientClaim]time.Time{}
	}
	l.ended[key] = l.clock()
}

// idle reports whether no session runs work on the engine: none is live,
// or every live one is tearing down.
func (l *sessionLedger) idle() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sessions <= len(l.tearingDown)
}

// sessionTearingDown records that a live session's teardown has begun: it
// still counts as live, for the remote cache's settle and bound, until
// sessionEnded, but runs no check's work any more, only its cleanup (idle).
func (l *sessionLedger) sessionTearingDown(sessionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tearingDown == nil {
		l.tearingDown = map[string]struct{}{}
	}
	l.tearingDown[sessionID] = struct{}{}
}

// attachRemote gives the ledger the remote cache's settle state, for an
// integration whose exports in progress exports counts, and that pause and
// resume give up and take up again. The ledger calls all three with its lock
// held, so that a pause and the resume of a session starting after it happen
// in order; none may call back into the ledger.
func (l *sessionLedger) attachRemote(exports func() int, pause, resume func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := &remoteSettle{exports: exports, pause: pause, resume: resume, bound: remoteCacheSettleBound}
	if l.sessions > 0 {
		r.period = 1
	}
	l.remote = r
	l.notifyLocked()
}

// detachRemote drops the remote cache's settle state: the engine then
// reports as an engine without the remote cache.
func (l *sessionLedger) detachRemote() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.remote != nil && l.remote.timer != nil {
		l.remote.timer.Stop()
	}
	l.remote = nil
	l.notifyLocked()
}

// settleTimedOut gives up the remote cache's work for period once its bound
// has passed, unless the engine settled, or a session started, first.
func (l *sessionLedger) settleTimedOut(r *remoteSettle, period uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.remote != r || r.period != period || l.sessions > 0 || r.timedOut || (len(r.unacked) == 0 && r.exports() == 0) {
		return
	}
	r.timedOut = true
	r.pause()
	l.notifyLocked()
}

// settledFor records the service's Settled for a settle of period that
// listed sessions: they are covered, unless the period ended, or the engine
// gave up on it, first.
func (l *sessionLedger) settledFor(period uint64, sessions []protocol.SettleSession) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.remote
	if r == nil || r.period != period || r.timedOut || l.sessions > 0 {
		return
	}
	r.unacked = slices.DeleteFunc(r.unacked, func(s protocol.SettleSession) bool {
		return slices.ContainsFunc(sessions, func(listed protocol.SettleSession) bool { return listed.ID == s.ID })
	})
	for _, s := range sessions {
		delete(l.claims, s.ID)
	}
	r.settled = true
	l.notifyLocked()
}

// settleState is where the engine stands in settling with the remote cache
// service, and a channel closed when that next changes.
func (l *sessionLedger) settleState() (remotecache.SettleState, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.changed == nil {
		l.changed = make(chan struct{})
	}
	r := l.remote
	if r == nil {
		return remotecache.SettleState{Live: l.sessions > 0}, l.changed
	}
	var claims []protocol.SessionClaim
	for id, c := range l.claims {
		claims = append(claims, protocol.SessionClaim{ID: id, Claim: c.claim, Started: c.started})
	}
	slices.SortFunc(claims, func(a, b protocol.SessionClaim) int { return strings.Compare(a.ID, b.ID) })
	return remotecache.SettleState{
		Period:   r.period,
		Live:     l.sessions > 0,
		Sessions: slices.Clone(r.unacked),
		Settled:  r.settled,
		TimedOut: r.timedOut,
		Claims:   claims,
	}, l.changed
}

// settling reports whether the engine is settling with the remote cache
// service: the integration is attached, no session is live, and the idle
// period has neither settled nor timed out. The channel is closed when that
// next changes. The settle's bound ends it: by then the engine has settled,
// timed out, or a session has started.
func (l *sessionLedger) settling() (bool, <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.changed == nil {
		l.changed = make(chan struct{})
	}
	r := l.remote
	return r != nil && l.sessions == 0 && !r.settled && !r.timedOut, l.changed
}

// awaitSettle returns once the engine isn't settling (settling), or when ctx
// ends.
func (l *sessionLedger) awaitSettle(ctx context.Context) {
	for {
		settling, changed := l.settling()
		if !settling {
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return
		}
	}
}

func (l *sessionLedger) notifyLocked() {
	if l.changed != nil {
		close(l.changed)
		l.changed = nil
	}
}

// status reports the ledger's clients, and the remote cache's state: busy
// while a session is live; otherwise timed out once the engine gave up
// settling, or settled once every ended session is covered and no export is
// in progress; otherwise busy. Without the remote cache, settled whenever no
// session is live.
func (l *sessionLedger) status(engineID string) EngineStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	status := EngineStatus{
		EngineID:    engineID,
		Open:        []StatusClient{},
		Ended:       []StatusEndedClient{},
		RemoteCache: StatusRemoteCache{State: RemoteCacheSettled},
	}
	for key := range l.live {
		status.Open = append(status.Open, StatusClient{ClientID: key.clientID, ClaimID: key.claimID})
	}
	for key, at := range l.ended {
		if now.Sub(at) >= endedClientRetention {
			delete(l.ended, key)
			continue
		}
		status.Ended = append(status.Ended, StatusEndedClient{ClientID: key.clientID, ClaimID: key.claimID, EndedAt: at})
	}
	switch r := l.remote; {
	case l.sessions > 0:
		status.RemoteCache.State = RemoteCacheBusy
	case r == nil:
	case r.timedOut:
		status.RemoteCache.State = RemoteCacheTimedOut
	case len(r.unacked) > 0 || r.exports() > 0:
		status.RemoteCache.State = RemoteCacheBusy
	}
	if l.remote != nil {
		status.RemoteCache.IdlePeriod = l.remote.period
	}
	slices.SortFunc(status.Open, func(a, b StatusClient) int {
		return strings.Compare(a.ClientID+"\x00"+a.ClaimID, b.ClientID+"\x00"+b.ClaimID)
	})
	slices.SortFunc(status.Ended, func(a, b StatusEndedClient) int {
		return strings.Compare(a.ClientID+"\x00"+a.ClaimID, b.ClientID+"\x00"+b.ClaimID)
	})
	return status
}

// ServeStatus answers GET StatusPath with the engine's EngineStatus. It needs
// no session and opens none.
func (srv *Server) ServeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(srv.sessionLedger.status(srv.engineName))
}
