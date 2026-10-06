package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/stretchr/testify/require"
)

// registerTestAttachables registers attachables for clientID on the context
// serveSessionAttachables gives them, and returns a channel that receives once
// they end.
func registerTestAttachables(t *testing.T, sess *daggerSession, clientID string) <-chan error {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })
	ended := make(chan error, 1)
	go func() {
		ctx := sess.withAttachablesCancel(context.Background(), clientID)
		ended <- sess.attachables.Register(ctx, clientID, serverConn, nil)
	}()
	require.Eventually(t, func() bool {
		_, ok := sess.attachables.Lookup(clientID)
		return ok
	}, 5*time.Second, time.Millisecond)
	return ended
}

// waitFor reports whether cond holds within timeout.
func waitFor(cond func() bool, timeout time.Duration) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

func requireAttachablesEnded(t *testing.T, sess *daggerSession, clientID string, ended <-chan error, msg string) {
	t.Helper()
	select {
	case err := <-ended:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal(msg)
	}
	_, ok := sess.attachables.Lookup(clientID)
	require.False(t, ok, msg)
}

// The main client's shutdown begins the session's closing before its final
// metric collection, and the final Cloud flush may refresh an expiring Cloud
// token by reading the credentials file through the main client's
// attachables. They must outlast the closing until that flush, and end with
// the shutdown. Other clients' attachables still end with the closing.
func TestMainClientAttachablesLastUntilFinalCloudFlush(t *testing.T) {
	t.Parallel()
	srv, sess, main, _, requestScope := newNestedTransportTestFixture(t)
	defer requestScope.Lease().Release()
	sess.services = core.NewServices()
	sess.shutdownCh = make(chan struct{})
	main.shutdownCh = make(chan struct{})
	sess.attachables = newSessionAttachableManager()
	sess.closingCtx, sess.cancelClosing = context.WithCancelCause(context.Background())
	sess.mainAttachablesCtx, sess.endMainAttachables = context.WithCancelCause(context.Background())

	mainEnded := registerTestAttachables(t, sess, main.clientID)
	otherEnded := registerTestAttachables(t, sess, "other")

	// A token near its expiry cannot outlive the client, so the shutdown flushes
	// Cloud before closing and once more at the end.
	sess.setCloudTokenExpiry(time.Now().Add(time.Second))
	var mu sync.Mutex
	var available []bool
	sess.cloudFlushers = []func(context.Context){func(context.Context) {
		ok := true
		if sess.closingCtx.Err() != nil {
			// The closing ends attachables asynchronously. Once it has ended the
			// other client's, the main client's must still be up for a while.
			ok = waitFor(func() bool {
				_, active := sess.attachables.Lookup("other")
				return !active
			}, 5*time.Second)
			for deadline := time.Now().Add(200 * time.Millisecond); ok && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
				_, ok = sess.attachables.Lookup(main.clientID)
			}
		} else {
			_, ok = sess.attachables.Lookup(main.clientID)
		}
		mu.Lock()
		available = append(available, ok)
		mu.Unlock()
	}}

	sess.scopeMu.Lock()
	shutdownLease := sess.newClientLifecycleLeaseLocked(main, engine.ClientLeaseRequest, "POST /shutdown")
	sess.scopeMu.Unlock()
	defer shutdownLease.Release()
	req := httptest.NewRequest(http.MethodPost, engine.ShutdownEndpoint, nil)
	require.NoError(t, srv.serveShutdown(httptest.NewRecorder(), req, main))

	require.Equal(t, []bool{true, true}, available,
		"the main client's attachables last through the final Cloud flush")
	requireAttachablesEnded(t, sess, "other", otherEnded, "the closing did not end another client's attachables")
	requireAttachablesEnded(t, sess, main.clientID, mainEnded, "the shutdown did not end the main client's attachables")
}

// A session removed without its main client's shutdown, such as when the
// client disconnects, still ends the main client's attachables.
func TestSessionRemovalEndsMainClientAttachables(t *testing.T) {
	srv := newTeardownTestServer(t)
	md := &engine.ClientMetadata{SessionID: "session", ClientID: "main"}
	client := &clientRuntime{clientRecord: &clientRecord{clientID: "main", clientMetadata: md, shutdownCh: make(chan struct{})}}
	sess := &daggerSession{
		sessionID:          md.SessionID,
		mainClientCallerID: md.ClientID,
		clientRuntimes:     map[string]*clientRuntime{client.clientID: client},
		services:           core.NewServices(),
		analytics:          analytics.New(analytics.Config{DoNotTrack: true}),
		containers:         map[bkgw.Container]struct{}{},
		shutdownCh:         make(chan struct{}),
		attachables:        newSessionAttachableManager(),
	}
	client.daggerSession = sess
	installTestClientRecords(sess)
	sess.dagqlCond = sync.NewCond(&sess.dagqlMu)
	sess.closingCtx, sess.cancelClosing = context.WithCancelCause(context.Background())
	sess.mainAttachablesCtx, sess.endMainAttachables = context.WithCancelCause(context.Background())

	ended := registerTestAttachables(t, sess, client.clientID)
	require.NoError(t, srv.removeDaggerSession(t.Context(), sess))
	requireAttachablesEnded(t, sess, client.clientID, ended, "session removal did not end the main client's attachables")
}
