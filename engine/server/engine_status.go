package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// StatusPath is where the engine's main listener serves its status, outside
// any session: GET answers EngineStatus.
const StatusPath = "/v1/status"

// endedClientRetention is how long the status keeps reporting a client
// whose sessions have all ended, unless a session with its ID starts again.
const endedClientRetention = 2 * time.Minute

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
	ended    map[clientClaim]time.Time
}

func (l *sessionLedger) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// sessionStarted counts a live root session of clientID under claimID.
func (l *sessionLedger) sessionStarted(clientID, claimID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.live == nil {
		l.live = map[clientClaim]int{}
	}
	l.live[clientClaim{clientID, claimID}]++
	l.sessions++
	for ended := range l.ended {
		if ended.clientID == clientID {
			delete(l.ended, ended)
		}
	}
}

// sessionEnded counts down a root session sessionStarted counted, and
// records its client as ended once no live session of it remains.
func (l *sessionLedger) sessionEnded(clientID, claimID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := clientClaim{clientID, claimID}
	if l.live[key] == 0 {
		return
	}
	l.sessions--
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

// status reports the ledger's clients, and the remote cache's state as an
// engine without the remote cache has it.
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
	if l.sessions > 0 {
		status.RemoteCache.State = RemoteCacheBusy
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
