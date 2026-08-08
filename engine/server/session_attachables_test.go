package server

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	"github.com/stretchr/testify/require"
)

func TestSessionAttachableWaitUnknownSourceCanPublish(t *testing.T) {
	t.Parallel()
	manager := newSessionAttachableManager()
	waiterAdded := make(chan struct{})
	manager.testWaiterAdded = func(string) { close(waiterAdded) }

	type result struct {
		caller engineutil.SessionCaller
		err    error
	}
	waitResult := make(chan result, 1)
	go func() {
		caller, err := manager.Wait(t.Context(), "source-client", nil)
		waitResult <- result{caller: caller, err: err}
	}()
	<-waiterAdded

	caller := &sessionAttachableCaller{ctx: context.Background()}
	manager.mu.Lock()
	manager.callers["source-client"] = caller
	manager.wakeWaitersLocked("source-client")
	manager.mu.Unlock()

	got := <-waitResult
	require.NoError(t, got.err)
	require.Same(t, caller, got.caller)
}

func TestSessionAttachableWaitPublishedSourceIsUnavailable(t *testing.T) {
	t.Parallel()
	sess := &daggerSession{
		lifetime:    sessionLifetimeDetachable,
		attachables: newSessionAttachableManager(),
	}
	sess.markSourceClientPublished("source-client")

	caller, err := sess.waitForClientCaller(t.Context(), "source-client")
	require.Nil(t, caller)
	var unavailable *engine.SourceClientUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Equal(t, "source-client", unavailable.ClientID)
}

func TestSessionAttachableWaitPrefersActivePublishedCaller(t *testing.T) {
	t.Parallel()
	sess := &daggerSession{
		lifetime:    sessionLifetimeDetachable,
		attachables: newSessionAttachableManager(),
	}
	sess.markSourceClientPublished("source-client")
	active := &sessionAttachableCaller{ctx: context.Background()}
	sess.attachables.callers["source-client"] = active

	caller, err := sess.waitForClientCaller(t.Context(), "source-client")
	require.NoError(t, err)
	require.Same(t, active, caller)
}

func TestSessionAttachableWaitRechecksTerminalAfterMissedCaller(t *testing.T) {
	t.Parallel()
	sess := &daggerSession{
		lifetime:    sessionLifetimeDetachable,
		attachables: newSessionAttachableManager(),
	}
	waiterAdded := make(chan struct{})
	waiterWoke := make(chan struct{})
	checkTerminal := make(chan struct{})
	sess.attachables.testWaiterAdded = func(string) { close(waiterAdded) }
	sess.attachables.testWaiterWoke = func(string) {
		close(waiterWoke)
		<-checkTerminal
	}

	waitResult := make(chan error, 1)
	go func() {
		_, err := sess.waitForClientCaller(t.Context(), "source-client")
		waitResult <- err
	}()
	<-waiterAdded

	caller := &sessionAttachableCaller{ctx: context.Background()}
	sess.attachables.mu.Lock()
	sess.attachables.callers["source-client"] = caller
	sess.attachables.wakeWaitersLocked("source-client")
	sess.attachables.mu.Unlock()
	<-waiterWoke

	// The caller can publish and be removed before the woken waiter gets the
	// manager lock. The terminal check must still recognize permanent loss.
	sess.markSourceClientPublished("source-client")
	sess.attachables.mu.Lock()
	delete(sess.attachables.callers, "source-client")
	sess.attachables.mu.Unlock()
	close(checkTerminal)

	var unavailable *engine.SourceClientUnavailableError
	require.ErrorAs(t, <-waitResult, &unavailable)
}

func TestSessionAttachableWaitOrdinarySourceKeepsBoundedError(t *testing.T) {
	t.Parallel()
	manager := newSessionAttachableManager()
	waitCause := errors.New("bounded wait ended")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(waitCause)

	caller, err := manager.Wait(ctx, "source-client", nil)
	require.Nil(t, caller)
	require.ErrorIs(t, err, waitCause)
	require.ErrorContains(t, err, `no active session attachables for client "source-client"`)
	var unavailable *engine.SourceClientUnavailableError
	require.NotErrorAs(t, err, &unavailable)
}

func TestSpecificClientAttachableConnIfAvailableIgnoresPublishedAbsence(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachableLifecycleTestSession(t, 0)
	sess.markSourceClientPublished(creator.clientID)
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		SessionID: sess.sessionID,
		ClientID:  creator.clientID,
	})

	conn, available, err := srv.SpecificClientAttachableConn(ctx, creator.clientID, core.SpecificClientAttachableConnOpts{
		IfAvailable: true,
	})
	require.NoError(t, err)
	require.False(t, available)
	require.Nil(t, conn)
	select {
	case <-sess.closingCtx.Done():
		t.Fatal("source lookup canceled the detachable session")
	default:
	}
}

func TestSpecificClientAttachableConnBlockingPublishedSourceIsUnavailable(t *testing.T) {
	t.Parallel()
	srv, sess, creator := newDetachableLifecycleTestSession(t, 0)
	sess.markSourceClientPublished(creator.clientID)
	creator.getClientCaller = sess.waitForClientCaller
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		SessionID: sess.sessionID,
		ClientID:  creator.clientID,
	})

	conn, available, err := srv.SpecificClientAttachableConn(ctx, creator.clientID, core.SpecificClientAttachableConnOpts{})
	require.Nil(t, conn)
	require.False(t, available)
	var unavailable *engine.SourceClientUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Equal(t, creator.clientID, unavailable.ClientID)
	select {
	case <-sess.closingCtx.Done():
		t.Fatal("blocking source lookup canceled the detachable session")
	default:
	}
}
