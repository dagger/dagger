package engineutil

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

type nestedTransportSessionHandler struct {
	registered atomic.Int32
	closed     atomic.Int32
	served     atomic.Int32
}

func (handler *nestedTransportSessionHandler) RegisterNestedClientTransportForExec(
	_ context.Context,
	metadata *engine.ClientMetadata,
	_ string,
	_ string,
) (*engine.NestedClientTransport, error) {
	handler.registered.Add(1)
	transport := engine.NewNestedClientTransport(func() { handler.closed.Add(1) })
	return transport, nil
}

func (handler *nestedTransportSessionHandler) ServeHTTPToNestedClient(
	http.ResponseWriter,
	*http.Request,
	*engine.NestedClientTransport,
	*engine.ClientMetadata,
	string,
	bool,
	dagql.AnyObjectResult,
	dagql.Typed,
) {
	handler.served.Add(1)
}

func TestNestedClientParentUsesHeldScopeNotContextMetadata(t *testing.T) {
	t.Parallel()

	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "workspace-owner",
	})
	lease := engine.NewClientLifecycleLease(engine.ClientLeaseRequest, "test", func() {}, nil)
	t.Cleanup(lease.Release)
	scope, err := engine.NewClientScope(&engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "executable-client",
	}, lease)
	require.NoError(t, err)
	ctx, err = engine.ContextWithClientScope(ctx, scope)
	require.NoError(t, err)

	parentClientID, err := engine.NestedClientParentID(ctx, "session")
	require.NoError(t, err)
	require.Equal(t, "executable-client", parentClientID)

	_, err = engine.NestedClientParentID(ctx, "other-session")
	require.ErrorContains(t, err, "does not match")
}

func TestContainerNestedTransportsAreExactPerRequestClient(t *testing.T) {
	t.Parallel()

	handler := &nestedTransportSessionHandler{}
	manager := newNestedClientTransportManager(t.Context(), handler, &engine.ClientMetadata{
		SessionID:         "session",
		ClientID:          "proxy-template",
		ClientSecretToken: "proxy-secret",
	}, "parent")

	request := func(metadata engine.ClientMetadata) (*engine.NestedClientTransport, *engine.ClientMetadata, int, error) {
		req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
		req.Header = metadata.AppendToHTTPHeaders(req.Header)
		return manager.transportForRequest(req)
	}

	first, firstMetadata, status, err := request(engine.ClientMetadata{
		ClientID:          "first",
		SessionID:         "untrusted-session",
		ClientSecretToken: "untrusted-secret",
	})
	require.NoError(t, err)
	require.Zero(t, status)
	require.Equal(t, "first", firstMetadata.ClientID)
	require.Equal(t, "session", firstMetadata.SessionID)
	require.Equal(t, "proxy-secret", firstMetadata.ClientSecretToken)
	require.False(t, first.Closed())
	require.Equal(t, int32(1), handler.registered.Load())

	same, sameMetadata, _, err := request(engine.ClientMetadata{ClientID: "first"})
	require.NoError(t, err)
	require.Same(t, first, same)
	require.Same(t, firstMetadata, sameMetadata)
	require.Equal(t, int32(1), handler.registered.Load())

	second, secondMetadata, _, err := request(engine.ClientMetadata{ClientID: "second"})
	require.NoError(t, err)
	require.NotSame(t, first, second)
	require.Equal(t, "second", secondMetadata.ClientID)
	require.Equal(t, int32(2), handler.registered.Load())

	first.Close()
	closed, _, _, err := request(engine.ClientMetadata{ClientID: "first"})
	require.NoError(t, err)
	require.Same(t, first, closed, "a closed client ID must never be rebound")
	require.True(t, closed.Closed())
	require.Equal(t, int32(2), handler.registered.Load())

	manager.Close()
	require.True(t, second.Closed())
	require.Equal(t, int32(2), handler.closed.Load())
	manager.Close()
	require.Equal(t, int32(2), handler.closed.Load(), "manager cleanup must be idempotent")

	_, _, status, err = request(engine.ClientMetadata{ClientID: "third"})
	require.Error(t, err)
	require.Equal(t, http.StatusGone, status)
}

func TestContainerNestedTransportManagerUsesExactHeaderlessProxyIdentity(t *testing.T) {
	t.Parallel()

	handler := &nestedTransportSessionHandler{}
	manager := newNestedClientTransportManager(t.Context(), handler, &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "proxy-template",
	}, "parent")

	req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
	transport, metadata, status, err := manager.transportForRequest(req)
	require.NoError(t, err)
	require.Equal(t, "proxy-template", metadata.ClientID)
	require.False(t, transport.Closed())
	require.Zero(t, status)
	require.Equal(t, int32(1), handler.registered.Load())

	malformed := httptest.NewRequest(http.MethodGet, "/query", nil)
	malformed.Header.Set(engine.ClientMetadataMetaKey, "not-base64")
	_, _, status, err = manager.transportForRequest(malformed)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, int32(1), handler.registered.Load(), "malformed metadata must not fall through to the proxy identity")

	emptyID := httptest.NewRequest(http.MethodGet, "/query", nil)
	emptyID.Header = (engine.ClientMetadata{}).AppendToHTTPHeaders(emptyID.Header)
	_, _, status, err = manager.transportForRequest(emptyID)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, int32(1), handler.registered.Load(), "an explicit empty ID must not fall through to the proxy identity")

	manager.Close()
	require.Equal(t, int32(1), handler.closed.Load())
	manager.Close()
	require.Equal(t, int32(1), handler.closed.Load())
}

// gatedRegistrationHandler blocks each registration until released so tests
// can hold a registration in flight while other proxy operations race it.
type gatedRegistrationHandler struct {
	nestedTransportSessionHandler
	entered  chan struct{}
	release  chan struct{}
	failNext atomic.Bool
}

func (handler *gatedRegistrationHandler) RegisterNestedClientTransportForExec(
	ctx context.Context,
	metadata *engine.ClientMetadata,
	parentClientID string,
	attachablesClientID string,
) (*engine.NestedClientTransport, error) {
	handler.entered <- struct{}{}
	<-handler.release
	if handler.failNext.CompareAndSwap(true, false) {
		return nil, errors.New("registration rejected")
	}
	return handler.nestedTransportSessionHandler.RegisterNestedClientTransportForExec(ctx, metadata, parentClientID, attachablesClientID)
}

type transportOutcome struct {
	transport *engine.NestedClientTransport
	status    int
	err       error
}

func requestNestedTransport(manager *nestedClientTransportManager, clientID string) <-chan transportOutcome {
	out := make(chan transportOutcome, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/query", nil)
		req.Header = (engine.ClientMetadata{ClientID: clientID}).AppendToHTTPHeaders(req.Header)
		transport, _, status, err := manager.transportForRequest(req)
		out <- transportOutcome{transport: transport, status: status, err: err}
	}()
	return out
}

func TestContainerNestedTransportManagerCloseDoesNotWaitForRegistration(t *testing.T) {
	t.Parallel()

	handler := &gatedRegistrationHandler{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	manager := newNestedClientTransportManager(t.Context(), handler, &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "proxy-template",
	}, "parent")

	first := requestNestedTransport(manager, "first")
	<-handler.entered

	// Exec cleanup closes the manager while the server-side registration is
	// still in flight. It must not wait for that registration: the session
	// serving it may itself be waiting for this exec's cleanup.
	closed := make(chan struct{})
	go func() {
		manager.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("manager.Close blocked behind an in-flight registration")
	}

	close(handler.release)
	outcome := <-first
	require.NoError(t, outcome.err)
	require.True(t, outcome.transport.Closed(), "a registration that completes after Close must close its transport")
	require.Equal(t, int32(1), handler.closed.Load())

	_, _, status, err := manager.transportForRequest(httptest.NewRequest(http.MethodGet, "/query", nil))
	require.Error(t, err)
	require.Equal(t, http.StatusGone, status)
}

func TestContainerNestedTransportManagerJoinsInFlightRegistration(t *testing.T) {
	t.Parallel()

	handler := &gatedRegistrationHandler{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	manager := newNestedClientTransportManager(t.Context(), handler, &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "proxy-template",
	}, "parent")

	first := requestNestedTransport(manager, "first")
	<-handler.entered
	// Requests for the same ID while its registration is in flight must join
	// that registration rather than start another one.
	joiners := make([]<-chan transportOutcome, 0, 4)
	for range 4 {
		joiners = append(joiners, requestNestedTransport(manager, "first"))
	}
	select {
	case outcome := <-joiners[0]:
		t.Fatalf("joiner returned before the registration finished: %+v", outcome)
	case <-time.After(50 * time.Millisecond):
	}

	close(handler.release)
	outcome := <-first
	require.NoError(t, outcome.err)
	require.False(t, outcome.transport.Closed())
	for _, joiner := range joiners {
		joined := <-joiner
		require.NoError(t, joined.err)
		require.Same(t, outcome.transport, joined.transport)
	}
	require.Equal(t, int32(1), handler.registered.Load(), "concurrent requests for one ID must register once")
}

func TestContainerNestedTransportManagerDoesNotCacheFailedRegistration(t *testing.T) {
	t.Parallel()

	handler := &gatedRegistrationHandler{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	close(handler.release)
	handler.failNext.Store(true)
	manager := newNestedClientTransportManager(t.Context(), handler, &engine.ClientMetadata{
		SessionID: "session",
		ClientID:  "proxy-template",
	}, "parent")

	request := func() (*engine.NestedClientTransport, int, error) {
		req := httptest.NewRequest(http.MethodGet, "/query", nil)
		req.Header = (engine.ClientMetadata{ClientID: "first"}).AppendToHTTPHeaders(req.Header)
		transport, _, status, err := manager.transportForRequest(req)
		return transport, status, err
	}

	_, status, err := request()
	require.ErrorContains(t, err, "registration rejected")
	require.Equal(t, http.StatusConflict, status, "a rejected registration must be terminal for SSE clients")

	transport, status, err := request()
	require.NoError(t, err)
	require.Zero(t, status)
	require.False(t, transport.Closed())
	require.Equal(t, int32(1), handler.registered.Load(), "the retried registration is the first successful one")

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			same, _, err := request()
			require.NoError(t, err)
			require.Same(t, transport, same)
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), handler.registered.Load())
}
