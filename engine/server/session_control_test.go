package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	testSessionID    = "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	testAttachmentID = "att_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	testObserverID   = "observer"
)

func newDetachableLifecycleTestSession(t *testing.T, activeCount int) (*Server, *daggerSession, *daggerClient) {
	t.Helper()
	srv := newTeardownTestServer(t)
	srv.sessionResultsRoot = t.TempDir()
	srv.terminalSessionIDs = map[string]time.Time{}
	srv.now = time.Now
	sess, creator := newTeardownTestSession(srv, testSessionID, "creator", activeCount)
	releaseTeardownDrain(sess)
	sess.lifetime = sessionLifetimeDetachable
	sess.creatorClientID = creator.clientID
	sess.createdAt = srv.nowTime()
	sess.attachables = newSessionAttachableManager()
	sess.nextGeneration = 1
	creator.secretToken = "token"
	creator.shutdownCh = make(chan struct{})
	return srv, sess, creator
}

func TestDetachableLastDisconnectPreservesSession(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachableLifecycleTestSession(t, 1)

	require.NoError(t, srv.releaseClientConnection(t.Context(), sess, creator))
	require.Equal(t, sessionStateInitialized, sess.state.Load())
	require.True(t, sessionInRegistry(srv, testSessionID))
	select {
	case <-sess.closingCtx.Done():
		t.Fatal("detachable disconnect canceled the session root")
	default:
	}
}

func TestHTTPHandlerPreservesSessionControlStatusAndCode(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://dagger/v1/sessions/"+testSessionID, nil)
	httpHandlerFunc(func(http.ResponseWriter, *http.Request, struct{}) error {
		return sessionNotFoundError(testSessionID)
	}, struct{}{}).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	var response engine.SessionProtocolErrorResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	require.Equal(t, engine.SessionErrorSessionNotFound, response.Error.Code)
}

func TestUnknownSessionControlRouteHasProtocolCode(t *testing.T) {
	t.Parallel()
	srv, _, _ := newDetachableLifecycleTestSession(t, 0)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://dagger"+engine.SessionsEndpoint+"/unknown/route", nil)
	require.NoError(t, srv.serveSessionControl(recorder, request))

	require.Equal(t, http.StatusNotFound, recorder.Code)
	var response engine.SessionProtocolErrorResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	require.Equal(t, engine.SessionErrorInvalidRequest, response.Error.Code)
}

func TestObserverClaimRolePartitionAndFailedClaimBaseline(t *testing.T) {
	t.Parallel()
	srv, sess, _ := newDetachableLifecycleTestSession(t, 0)
	sess.currentAttachment = &sessionAttachment{
		ID: testAttachmentID, Generation: 1, ClientID: sess.creatorClientID, Ready: true,
	}
	clientsBefore := len(sess.clients)
	callersBefore := len(sess.attachables.callers)
	reservationsBefore := len(sess.attachables.reservations)

	opts := &ClientInitOpts{ClientMetadata: &engine.ClientMetadata{
		SessionID: testSessionID, ClientID: testObserverID, ClientSecretToken: "observer-token",
		AttachSession: true, AttachmentID: "att_bbbbbbbbbbbbbbbbbbbbbbbbbb",
	}}
	_, err := srv.claimObserverAttachment(opts)
	var protocolErr sessionControlError
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, engine.SessionErrorAlreadyAttached, protocolErr.code)
	require.Len(t, sess.clients, clientsBefore, "failed claim created an orphan observer client")
	require.Len(t, sess.attachables.callers, callersBefore)
	require.Len(t, sess.attachables.reservations, reservationsBefore)

	creatorOpts := &ClientInitOpts{ClientMetadata: &engine.ClientMetadata{
		SessionID: testSessionID, ClientID: sess.creatorClientID, ClientSecretToken: "token",
		DetachableSession: true, AttachmentID: testAttachmentID,
	}}
	sess.currentAttachment = &sessionAttachment{
		ID: "att_cccccccccccccccccccccccccc", Generation: 2, ClientID: testObserverID, Ready: true,
	}
	_, err = srv.claimCreatorAttachment(sess, creatorOpts)
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, engine.SessionErrorAlreadyAttached, protocolErr.code)
	require.Equal(t, testObserverID, sess.currentAttachment.ClientID, "creator stole the observer attachment")
}

func TestConcurrentObserverClaimsHaveOneWinner(t *testing.T) {
	t.Parallel()
	srv, sess, _ := newDetachableLifecycleTestSession(t, 0)
	hooks := &sessionTestHooks{Events: make(chan sessionTestEvent, 16)}
	barrier := make(chan struct{})
	hooks.setBarrier(sessionHookObserverAfterValidation, barrier)
	srv.sessionTestHooks = hooks

	opts := []*ClientInitOpts{
		{ClientMetadata: &engine.ClientMetadata{SessionID: testSessionID, ClientID: "observer-a", ClientSecretToken: "a", AttachSession: true, AttachmentID: "att_bbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		{ClientMetadata: &engine.ClientMetadata{SessionID: testSessionID, ClientID: "observer-b", ClientSecretToken: "b", AttachSession: true, AttachmentID: "att_cccccccccccccccccccccccccc"}},
	}
	type result struct {
		claim *sessionAttachmentClaim
		err   error
	}
	results := make(chan result, 2)
	for _, opt := range opts {
		go func() {
			claim, err := srv.claimObserverAttachment(opt)
			results <- result{claim: claim, err: err}
		}()
	}
	waitForSessionHookCount(t, hooks.Events, sessionHookObserverAfterValidation, 2)
	close(barrier)

	var winner *sessionAttachmentClaim
	losers := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			winner = result.claim
			continue
		}
		var protocolErr sessionControlError
		require.ErrorAs(t, result.err, &protocolErr)
		require.Equal(t, engine.SessionErrorAlreadyAttached, protocolErr.code)
		losers++
	}
	require.NotNil(t, winner)
	require.Equal(t, 1, losers)
	require.True(t, srv.detachAttachment(sess, winner.ID, winner.Generation))
}

func TestObserverClaimRacingTerminationCannotRecreateSession(t *testing.T) {
	t.Parallel()
	srv, sess, _ := newDetachableLifecycleTestSession(t, 0)
	hooks := &sessionTestHooks{Events: make(chan sessionTestEvent, 16)}
	barrier := make(chan struct{})
	hooks.setBarrier(sessionHookObserverAfterValidation, barrier)
	srv.sessionTestHooks = hooks

	opts := &ClientInitOpts{ClientMetadata: &engine.ClientMetadata{
		SessionID: testSessionID, ClientID: testObserverID, ClientSecretToken: "observer-token",
		AttachSession: true, AttachmentID: "att_bbbbbbbbbbbbbbbbbbbbbbbbbb",
	}}
	claimResult := make(chan error, 1)
	go func() {
		_, err := srv.claimObserverAttachment(opts)
		claimResult <- err
	}()
	waitForSessionHookCount(t, hooks.Events, sessionHookObserverAfterValidation, 1)

	done := srv.startSessionTermination(testSessionID)
	require.NotNil(t, done)
	require.True(t, sess.terminating.Load())
	close(barrier)

	var protocolErr sessionControlError
	require.ErrorAs(t, <-claimResult, &protocolErr)
	require.Equal(t, engine.SessionErrorSessionTerminating, protocolErr.code)
	<-done
	require.False(t, sessionInRegistry(srv, testSessionID))
	require.Nil(t, sess.currentAttachment)
	require.Len(t, sess.clients, 1, "failed observer claim left an orphan client")

	_, err := srv.claimObserverAttachment(opts)
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, engine.SessionErrorSessionNotFound, protocolErr.code)
	require.False(t, sessionInRegistry(srv, testSessionID), "observer metadata recreated a terminated session")
}

func TestDelayedCreatorReconnectCannotStealObserver(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachableLifecycleTestSession(t, 0)
	sess.currentAttachment = &sessionAttachment{
		ID: testAttachmentID, Generation: 1, ClientID: creator.clientID, Ready: true,
	}
	require.True(t, srv.detachAttachment(sess, testAttachmentID, 1), "creator death did not clear attachment")

	observerAttachmentID := "att_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	observerOpts := &ClientInitOpts{ClientMetadata: &engine.ClientMetadata{
		SessionID: testSessionID, ClientID: testObserverID, ClientSecretToken: "observer-token",
		AttachSession: true, AttachmentID: observerAttachmentID,
	}}
	observerClaim, err := srv.claimObserverAttachment(observerOpts)
	require.NoError(t, err)
	sess.currentAttachment.Ready = true
	observerAttachment := sess.currentAttachment

	creatorOpts := &ClientInitOpts{ClientMetadata: &engine.ClientMetadata{
		SessionID: testSessionID, ClientID: creator.clientID, ClientSecretToken: creator.secretToken,
		DetachableSession: true, AttachmentID: testAttachmentID,
	}}
	_, err = srv.claimCreatorAttachment(sess, creatorOpts)
	var protocolErr sessionControlError
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, engine.SessionErrorAlreadyAttached, protocolErr.code)
	require.Same(t, observerAttachment, sess.currentAttachment)
	require.Equal(t, observerClaim.ID, sess.currentAttachment.ID)
	require.Equal(t, observerClaim.Generation, sess.currentAttachment.Generation)
	require.Equal(t, observerClaim.ClientID, sess.currentAttachment.ClientID)
}

func TestRepeatedObserverDetachReturnsResourcesToBaseline(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachableLifecycleTestSession(t, 0)
	dbRoot := t.TempDir()
	srv.clientDBs = clientdb.NewDBs(dbRoot)
	hooks := &sessionTestHooks{Events: make(chan sessionTestEvent, 64)}
	srv.sessionTestHooks = hooks

	const attempts = 4
	for i, clientID := range []string{"observer-a", "observer-b", "observer-c", "observer-d"} {
		attachmentID := "att_" + strings.Repeat(string(rune('b'+i)), engine.SessionIDEncodedLength)
		claim, err := srv.claimObserverAttachment(&ClientInitOpts{ClientMetadata: &engine.ClientMetadata{
			SessionID: testSessionID, ClientID: clientID, ClientSecretToken: "token",
			AttachSession: true, AttachmentID: attachmentID,
		}})
		require.NoError(t, err)

		keepAlive, err := srv.clientDBs.Open(t.Context(), clientID)
		require.NoError(t, err)
		exporter := &shutdownRecordingSpanExporter{done: make(chan struct{})}
		observer := &daggerClient{
			daggerSession: sess, clientID: clientID, observerClient: true,
			keepAliveTelemetryDB: keepAlive, shutdownCh: make(chan struct{}),
			tracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter)),
		}
		sess.clientMu.Lock()
		sess.clients[clientID] = observer
		sess.clientMu.Unlock()

		require.True(t, srv.detachAttachment(sess, claim.ID, claim.Generation))
		require.Nil(t, observer.keepAliveTelemetryDB)
		select {
		case <-exporter.done:
		default:
			t.Fatal("observer tracer provider was not shut down")
		}
		select {
		case <-observer.shutdownCh:
		default:
			t.Fatal("observer shutdown channel was not closed")
		}
		sess.clientMu.RLock()
		require.Equal(t, map[string]*daggerClient{creator.clientID: creator}, sess.clients)
		sess.clientMu.RUnlock()
		require.Nil(t, sess.currentAttachment)

		entries, err := os.ReadDir(dbRoot)
		require.NoError(t, err)
		old := time.Now().Add(-clientdb.CollectGarbageAfter - time.Minute)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), clientID) {
				require.NoError(t, os.Chtimes(dbRoot+"/"+entry.Name(), old, old))
			}
		}
		require.NoError(t, srv.clientDBs.GC(map[string]bool{creator.clientID: true}))
		entries, err = os.ReadDir(dbRoot)
		require.NoError(t, err)
		for _, entry := range entries {
			require.False(t, strings.HasPrefix(entry.Name(), clientID), "observer telemetry store remained open")
		}
	}

	close(hooks.Events)
	cleanupCount := 0
	for event := range hooks.Events {
		if event.Name == sessionHookObserverCleanup {
			cleanupCount++
		}
	}
	require.Equal(t, attempts, cleanupCount)
}

func TestObserverCleanupPreservesSameIDReplacement(t *testing.T) {
	t.Parallel()
	srv, sess, _ := newDetachableLifecycleTestSession(t, 0)
	hooks := &sessionTestHooks{Events: make(chan sessionTestEvent, 16)}
	barrier := make(chan struct{})
	hooks.setBarrier(sessionHookAttachmentCleared, barrier)
	srv.sessionTestHooks = hooks

	old := &daggerClient{
		daggerSession: sess, clientID: testObserverID, observerClient: true,
		shutdownCh: make(chan struct{}),
	}
	sess.clients[testObserverID] = old
	sess.currentAttachment = &sessionAttachment{
		ID: testAttachmentID, Generation: 2, ClientID: testObserverID, Ready: true,
	}
	detached := make(chan bool, 1)
	go func() { detached <- srv.detachAttachment(sess, testAttachmentID, 2) }()
	waitForSessionHookCount(t, hooks.Events, sessionHookAttachmentCleared, 1)

	replacement := &daggerClient{
		daggerSession: sess, clientID: testObserverID, observerClient: true,
		shutdownCh: make(chan struct{}),
	}
	sess.clientMu.Lock()
	sess.clients[testObserverID] = replacement
	sess.clientMu.Unlock()
	close(barrier)
	require.True(t, <-detached)

	sess.clientMu.RLock()
	require.Same(t, replacement, sess.clients[testObserverID])
	sess.clientMu.RUnlock()
	select {
	case <-old.shutdownCh:
	default:
		t.Fatal("detached observer was not cleaned up")
	}
	select {
	case <-replacement.shutdownCh:
		t.Fatal("replacement observer was cleaned up by a delayed detach")
	default:
	}
}

func TestAttachmentIDAndGenerationRejectDelayedEvents(t *testing.T) {
	t.Parallel()
	srv, sess, _ := newDetachableLifecycleTestSession(t, 0)
	sess.currentAttachment = &sessionAttachment{
		ID: "att_bbbbbbbbbbbbbbbbbbbbbbbbbb", Generation: 2, ClientID: testObserverID, Ready: true,
	}
	require.False(t, srv.detachAttachment(sess, testAttachmentID, 1))
	require.False(t, srv.detachAttachment(sess, "att_bbbbbbbbbbbbbbbbbbbbbbbbbb", 1))
	require.NotNil(t, sess.currentAttachment)
	require.True(t, srv.detachAttachment(sess, "att_bbbbbbbbbbbbbbbbbbbbbbbbbb", 2))
	require.Nil(t, sess.currentAttachment)
}

func TestTerminateInstallsTombstoneBeforeCreatorRetry(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachableLifecycleTestSession(t, 0)
	fakeNow := time.Now()
	srv.now = func() time.Time { return fakeNow }
	hooks := &sessionTestHooks{Events: make(chan sessionTestEvent, 32)}
	barrier := make(chan struct{})
	hooks.setBarrier(sessionHookTerminalTransition, barrier)
	srv.sessionTestHooks = hooks

	doneResult := make(chan (<-chan struct{}), 1)
	go func() { doneResult <- srv.startSessionTermination(testSessionID) }()
	waitForSessionHookCount(t, hooks.Events, sessionHookTerminalTransition, 1)
	require.True(t, srv.terminalSessionIDExists(testSessionID))

	_, _, err := srv.getOrInitClient(t.Context(), &ClientInitOpts{ClientMetadata: &engine.ClientMetadata{
		SessionID: testSessionID, ClientID: creator.clientID, ClientSecretToken: creator.secretToken,
		DetachableSession: true, AttachmentID: testAttachmentID,
	}})
	var protocolErr sessionControlError
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, engine.SessionErrorSessionTerminated, protocolErr.code)

	close(barrier)
	done := <-doneResult
	<-done
	require.False(t, sessionInRegistry(srv, testSessionID))
	_, _, err = srv.getOrInitClient(t.Context(), &ClientInitOpts{ClientMetadata: &engine.ClientMetadata{
		SessionID: testSessionID, ClientID: creator.clientID, ClientSecretToken: creator.secretToken,
		DetachableSession: true, AttachmentID: testAttachmentID,
	}})
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, engine.SessionErrorSessionTerminated, protocolErr.code)

	fakeNow = fakeNow.Add(terminalSessionTombstoneTTL + time.Second)
	require.False(t, srv.terminalSessionIDExists(testSessionID), "expired tombstone retained control state")
	_ = sess
}

func TestConcurrentTerminateSharesOneCleanup(t *testing.T) {
	t.Parallel()
	srv, _, _ := newDetachableLifecycleTestSession(t, 0)
	resultDir, err := srv.sessionResultDir(testSessionID)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(resultDir, 0o700))
	require.NoError(t, os.WriteFile(resultDir+"/"+detachedResultFilename, []byte("saved"), 0o600))
	hooks := &sessionTestHooks{Events: make(chan sessionTestEvent, 64)}
	srv.sessionTestHooks = hooks

	doneA := srv.startSessionTermination(testSessionID)
	doneB := srv.startSessionTermination(testSessionID)
	require.Equal(t, doneA, doneB)
	<-doneA

	close(hooks.Events)
	counts := map[string]int{}
	for event := range hooks.Events {
		if event.Name == sessionHookResourceCleanup {
			counts[event.Resource]++
		}
		if event.Name == sessionHookRegistryDelete {
			counts[sessionHookRegistryDelete]++
		}
	}
	for _, resource := range []string{"services", "resolver", "containers-and-clients", "analytics", "dagql-cache", "results"} {
		require.Equalf(t, 1, counts[resource], "resource %s cleanup count", resource)
	}
	require.Equal(t, 1, counts[sessionHookRegistryDelete])
	_, err = os.Stat(resultDir)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestManagementDeleteDoesNotTerminateOrdinarySession(t *testing.T) {
	t.Parallel()
	srv := newTeardownTestServer(t)
	sess, _ := newTeardownTestSession(srv, testSessionID, "ordinary", 1)
	done := srv.startSessionTermination(testSessionID)
	require.Nil(t, done)
	require.Equal(t, sessionStateInitialized, sess.state.Load())
	require.True(t, sessionInRegistry(srv, testSessionID))
	releaseTeardownDrain(sess)
}

func TestSessionSnapshotDoesNotWaitForLifecycleLock(t *testing.T) {
	t.Parallel()
	srv, sess, _ := newDetachableLifecycleTestSession(t, 0)
	sess.lifecycleMu.Lock()
	defer sess.lifecycleMu.Unlock()
	done := make(chan struct{})
	go func() {
		_, ok := srv.snapshotDetachableSession(sess)
		require.True(t, ok)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session snapshot blocked on lifecycleMu")
	}
}

func TestNestedMetadataClearsDetachableRoles(t *testing.T) {
	t.Parallel()
	metadata := &engine.ClientMetadata{
		SessionID: testSessionID, DetachableSession: true, AttachSession: true,
		AttachmentID: testAttachmentID,
	}
	nested := nestedClientMetadataForRequest(http.Header{}, metadata)
	require.False(t, nested.DetachableSession)
	require.False(t, nested.AttachSession)
	require.Empty(t, nested.AttachmentID)
}

func TestNestedClientCannotAccessSessionControlRoutes(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachableLifecycleTestSession(t, 0)
	attachment := &sessionAttachment{
		ID: testAttachmentID, Generation: 2, ClientID: creator.clientID, Ready: true,
	}
	sess.currentAttachment = attachment
	sess.nextGeneration = attachment.Generation
	metadata := &engine.ClientMetadata{
		SessionID: testSessionID, ClientID: "nested", ClientSecretToken: "nested-token",
		ClientVersion: engine.Version,
	}

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "list", method: http.MethodGet, path: engine.SessionsEndpoint},
		{name: "inspect", method: http.MethodGet, path: engine.SessionsEndpoint + "/" + testSessionID},
		{name: "close attachment", method: http.MethodDelete, path: engine.SessionsEndpoint + "/" + testSessionID + "/attachments/" + testAttachmentID},
		{name: "submit query", method: http.MethodPost, path: engine.SessionsEndpoint + "/" + testSessionID + "/queries/primary"},
		{name: "inspect query", method: http.MethodGet, path: engine.SessionsEndpoint + "/" + testSessionID + "/queries/primary"},
		{name: "read result", method: http.MethodGet, path: engine.SessionsEndpoint + "/" + testSessionID + "/queries/primary/result"},
		{name: "read telemetry", method: http.MethodGet, path: engine.SessionsEndpoint + "/" + testSessionID + "/telemetry/traces"},
		{name: "terminate", method: http.MethodDelete, path: engine.SessionsEndpoint + "/" + testSessionID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, "http://dagger"+test.path, nil)
			srv.ServeHTTPToNestedClient(recorder, request, metadata, creator.clientID, false, nil, nil)

			require.Equal(t, http.StatusForbidden, recorder.Code)
			var response engine.SessionProtocolErrorResponse
			require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
			require.Equal(t, engine.SessionErrorInvalidRequest, response.Error.Code)
			require.True(t, sessionInRegistry(srv, testSessionID))
			require.Same(t, attachment, sess.currentAttachment)
			require.Nil(t, sess.primaryQuery)
			require.False(t, sess.terminating.Load())
		})
	}
}

func TestDetachableAttachmentShutdownRejectedButNestedShutdownSucceeds(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachableLifecycleTestSession(t, 0)
	creator.clientMetadata = &engine.ClientMetadata{DetachableSession: true}
	attachment := &sessionAttachment{
		ID: testAttachmentID, Generation: 2, ClientID: creator.clientID, Ready: true,
	}
	sess.currentAttachment = attachment

	observer := &daggerClient{
		daggerSession: sess, clientID: testObserverID, observerClient: true,
		shutdownCh: make(chan struct{}),
	}
	for name, client := range map[string]*daggerClient{
		"creator":  creator,
		"observer": observer,
	} {
		t.Run(name+" attachment", func(t *testing.T) {
			err := srv.serveShutdown(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, engine.ShutdownEndpoint, nil), client)
			var protocolErr sessionControlError
			require.ErrorAs(t, err, &protocolErr)
			require.Equal(t, http.StatusBadRequest, protocolErr.status)
			require.Equal(t, engine.SessionErrorInvalidRequest, protocolErr.code)
			select {
			case <-client.shutdownCh:
				t.Fatal("attachment shutdown channel was closed")
			default:
			}
		})
	}

	nested := &daggerClient{
		daggerSession: sess, clientID: "nested", shutdownCh: make(chan struct{}),
	}
	sess.clientMu.Lock()
	sess.clients[nested.clientID] = nested
	sess.clientMu.Unlock()
	recorder := httptest.NewRecorder()
	require.NoError(t, srv.serveShutdown(recorder, httptest.NewRequest(http.MethodPost, engine.ShutdownEndpoint, nil), nested))
	select {
	case <-nested.shutdownCh:
	default:
		t.Fatal("nested shutdown channel was not closed")
	}
	require.Same(t, attachment, sess.currentAttachment)
	require.True(t, sessionInRegistry(srv, testSessionID))
	select {
	case <-sess.closingCtx.Done():
		t.Fatal("nested shutdown canceled the detachable session")
	default:
	}
	select {
	case <-creator.shutdownCh:
		t.Fatal("nested shutdown closed the creator")
	default:
	}
}

func TestSupersededRegistrationDoesNotEmitRemoved(t *testing.T) {
	t.Parallel()
	manager := newSessionAttachableManager()

	oldServer, oldPeer := net.Pipe()
	oldConn := &gatedCloseConn{
		Conn: oldServer, closeStarted: make(chan struct{}), closeRelease: make(chan struct{}),
	}
	t.Cleanup(func() {
		oldConn.release()
		_ = oldPeer.Close()
	})
	oldCtx, cancelOld := context.WithCancelCause(t.Context())
	oldPublished := make(chan struct{})
	oldRemoved := make(chan error, 1)
	oldResult := make(chan error, 1)
	require.NoError(t, manager.Reserve(testObserverID))
	go func() {
		oldResult <- manager.Register(oldCtx, testObserverID, oldConn, nil, sessionAttachableCallbacks{
			Published: func() { close(oldPublished) },
			Removed:   func(err error) { oldRemoved <- err },
		})
	}()
	<-oldPublished

	cancelOld(errors.New("old registration lost transport"))
	<-oldConn.closeStarted
	require.NoError(t, manager.Reserve(testObserverID))

	newServer, newPeer := net.Pipe()
	t.Cleanup(func() { _ = newPeer.Close() })
	newCtx, cancelNew := context.WithCancelCause(t.Context())
	newPublished := make(chan struct{})
	newRemoved := make(chan error, 1)
	newResult := make(chan error, 1)
	go func() {
		newResult <- manager.Register(newCtx, testObserverID, newServer, nil, sessionAttachableCallbacks{
			Published: func() { close(newPublished) },
			Removed:   func(err error) { newRemoved <- err },
		})
	}()
	<-newPublished

	oldConn.release()
	require.NoError(t, <-oldResult)
	select {
	case err := <-oldRemoved:
		t.Fatalf("superseded registration emitted Removed: %v", err)
	default:
	}
	_, ok := manager.Lookup(testObserverID)
	require.True(t, ok, "old cleanup removed the replacement registration")

	newCause := errors.New("replacement closed")
	cancelNew(newCause)
	require.NoError(t, <-newResult)
	require.ErrorIs(t, <-newRemoved, newCause)
}

func TestCreatorRetryWaitsForRemovalThenPublishesFreshGeneration(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachableLifecycleTestSession(t, 0)
	creator.clientMetadata = &engine.ClientMetadata{
		SessionID: testSessionID, ClientID: creator.clientID, ClientSecretToken: creator.secretToken,
		DetachableSession: true, AttachmentID: testAttachmentID,
	}
	sess.currentAttachment = &sessionAttachment{
		ID: testAttachmentID, Generation: 2, ClientID: creator.clientID, Ready: true,
	}
	sess.nextGeneration = 2
	opts := &ClientInitOpts{ClientMetadata: creator.clientMetadata}

	_, err := srv.claimCreatorAttachment(sess, opts)
	var protocolErr sessionControlError
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, engine.SessionErrorAttachmentConnectionExists, protocolErr.code)
	require.Nil(t, opts.claimedAttachment)

	require.True(t, srv.detachAttachment(sess, testAttachmentID, 2))
	claim, err := srv.claimCreatorAttachment(sess, opts)
	require.NoError(t, err)
	require.Equal(t, uint64(3), claim.Generation)
	require.Same(t, claim, opts.claimedAttachment)
	require.False(t, sess.currentAttachment.Ready)

	hooks := &sessionTestHooks{Events: make(chan sessionTestEvent, 16)}
	srv.sessionTestHooks = hooks
	requestCtx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodGet, "http://dagger"+engine.SessionAttachablesEndpoint, nil).WithContext(requestCtx)
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = peerConn.Close() })
	handshake := make(chan error, 1)
	go func() {
		_, readErr := http.ReadResponse(bufio.NewReader(peerConn), request)
		if readErr == nil {
			_, readErr = peerConn.Write([]byte{1})
		}
		handshake <- readErr
		<-requestCtx.Done()
		_ = peerConn.Close()
	}()
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- srv.serveSessionAttachables(&attachmentFaultWriter{conn: serverConn}, request, sessionAttachablesRequest{
			client: creator, claim: claim,
		})
	}()
	require.NoError(t, <-handshake)
	waitForSessionHookCount(t, hooks.Events, sessionHookAttachmentPublished, 1)
	require.Equal(t, uint64(3), sess.currentAttachment.Generation)
	require.True(t, sess.currentAttachment.Ready)

	cancel()
	select {
	case err := <-serveResult:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("fresh creator registration did not stop")
	}
}

func TestPrePublicationAttachmentFailuresReleaseClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		writer func(*http.Request) http.ResponseWriter
	}{
		{name: "hijack", writer: func(*http.Request) http.ResponseWriter {
			return &attachmentFaultWriter{hijackErr: errors.New("hijack failed")}
		}},
		{name: "switching protocols write", writer: func(*http.Request) http.ResponseWriter {
			server, peer := net.Pipe()
			peer.Close()
			return &attachmentFaultWriter{conn: server}
		}},
		{name: "acknowledgment", writer: func(req *http.Request) http.ResponseWriter {
			server, peer := net.Pipe()
			go func() {
				resp, _ := http.ReadResponse(bufio.NewReader(peer), req)
				if resp != nil && resp.Body != nil {
					resp.Body.Close()
				}
				peer.Close()
			}()
			return &attachmentFaultWriter{conn: server}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			srv, sess, creator := newDetachableLifecycleTestSession(t, 1)
			attachmentID := "att_bbbbbbbbbbbbbbbbbbbbbbbbbb"
			sess.currentAttachment = &sessionAttachment{ID: attachmentID, Generation: 2, ClientID: testObserverID}
			observer := &daggerClient{
				daggerSession: sess, clientID: testObserverID, observerClient: true,
				clientMetadata: &engine.ClientMetadata{AttachSession: true}, shutdownCh: make(chan struct{}),
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://dagger"+engine.SessionAttachablesEndpoint, nil)
			require.NoError(t, err)
			err = srv.serveSessionAttachables(test.writer(req), req, sessionAttachablesRequest{
				client: observer,
				claim:  &sessionAttachmentClaim{ID: attachmentID, Generation: 2, ClientID: testObserverID},
			})
			require.Error(t, err)
			require.Nil(t, sess.currentAttachment)
			require.Empty(t, sess.attachables.reservations)
			_ = creator
		})
	}
}

func TestReservationConflictReleasesAttachmentClaim(t *testing.T) {
	t.Parallel()
	srv, sess, _ := newDetachableLifecycleTestSession(t, 1)
	attachmentID := "att_bbbbbbbbbbbbbbbbbbbbbbbbbb"
	sess.currentAttachment = &sessionAttachment{
		ID: attachmentID, Generation: 2, ClientID: testObserverID,
	}
	require.NoError(t, sess.attachables.Reserve(testObserverID))
	t.Cleanup(func() { sess.attachables.ReleaseReservation(testObserverID) })
	observer := &daggerClient{
		daggerSession: sess, clientID: testObserverID, observerClient: true,
		clientMetadata: &engine.ClientMetadata{AttachSession: true}, shutdownCh: make(chan struct{}),
	}
	req := httptest.NewRequest(http.MethodGet, "http://dagger"+engine.SessionAttachablesEndpoint, nil)
	err := srv.serveSessionAttachables(httptest.NewRecorder(), req, sessionAttachablesRequest{
		client: observer,
		claim:  &sessionAttachmentClaim{ID: attachmentID, Generation: 2, ClientID: testObserverID},
	})
	var protocolErr sessionControlError
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, engine.SessionErrorAttachmentConnectionExists, protocolErr.code)
	require.Nil(t, sess.currentAttachment)
	require.Len(t, sess.attachables.reservations, 1, "claim cleanup released another request's reservation")
}

func TestExplicitCloseOfReservationPreventsLateRegistration(t *testing.T) {
	t.Parallel()
	manager := newSessionAttachableManager()
	require.NoError(t, manager.Reserve(testObserverID))
	require.True(t, manager.Deregister(testObserverID))

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})
	err := manager.Register(t.Context(), testObserverID, serverConn, nil, sessionAttachableCallbacks{})
	require.ErrorIs(t, err, errAttachableExplicitClose)
	require.Empty(t, manager.callers)
	require.Empty(t, manager.reservations)
}

func waitForSessionHookCount(t *testing.T, events <-chan sessionTestEvent, name string, count int) {
	t.Helper()
	seen := 0
	for seen < count {
		select {
		case event := <-events:
			if event.Name == name {
				seen++
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for hook %s (%d/%d)", name, seen, count)
		}
	}
}

type attachmentFaultWriter struct {
	header    http.Header
	conn      net.Conn
	hijackErr error
}

func (writer *attachmentFaultWriter) Header() http.Header {
	if writer.header == nil {
		writer.header = make(http.Header)
	}
	return writer.header
}

func (*attachmentFaultWriter) Write(data []byte) (int, error) { return len(data), nil }
func (*attachmentFaultWriter) WriteHeader(int)                {}

func (writer *attachmentFaultWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if writer.hijackErr != nil {
		return nil, nil, writer.hijackErr
	}
	return writer.conn, bufio.NewReadWriter(bufio.NewReader(writer.conn), bufio.NewWriter(writer.conn)), nil
}

var _ http.Hijacker = (*attachmentFaultWriter)(nil)

type gatedCloseConn struct {
	net.Conn
	closeStarted chan struct{}
	closeRelease chan struct{}
	startedOnce  sync.Once
	releaseOnce  sync.Once
}

func (conn *gatedCloseConn) Close() error {
	conn.startedOnce.Do(func() { close(conn.closeStarted) })
	<-conn.closeRelease
	return conn.Conn.Close()
}

func (conn *gatedCloseConn) release() {
	conn.releaseOnce.Do(func() { close(conn.closeRelease) })
}

type shutdownRecordingSpanExporter struct {
	done chan struct{}
}

func (*shutdownRecordingSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return nil
}

func (exporter *shutdownRecordingSpanExporter) Shutdown(context.Context) error {
	close(exporter.done)
	return nil
}
