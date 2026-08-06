package client

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestAttachmentRetryRolePartition(t *testing.T) {
	t.Parallel()

	protocolErr := func(code string) error {
		return &SessionProtocolError{StatusCode: http.StatusConflict, Code: code, Message: code}
	}
	transportErr := errors.New("transport stopped")

	tests := []struct {
		name         string
		role         attachmentRetryRole
		errors       []error
		wantAttempts int
		wantWaits    int
		wantError    string
	}{
		{name: "creator retries own registration", role: attachmentRetryCreator, errors: []error{protocolErr(engine.SessionErrorAttachmentConnectionExists), nil}, wantAttempts: 2},
		{name: "creator does not steal observer", role: attachmentRetryCreator, errors: []error{protocolErr(engine.SessionErrorAlreadyAttached)}, wantAttempts: 1, wantError: engine.SessionErrorAlreadyAttached},
		{name: "observer retries occupied slot", role: attachmentRetryObserver, errors: []error{protocolErr(engine.SessionErrorAlreadyAttached), protocolErr(engine.SessionErrorAlreadyAttached), nil}, wantAttempts: 3, wantWaits: 1},
		{name: "observer rejects creator code", role: attachmentRetryObserver, errors: []error{protocolErr(engine.SessionErrorAttachmentConnectionExists)}, wantAttempts: 1, wantError: engine.SessionErrorAttachmentConnectionExists},
		{name: "creator transport fail fast", role: attachmentRetryCreator, errors: []error{transportErr}, wantAttempts: 1, wantError: transportErr.Error()},
		{name: "observer transport fail fast", role: attachmentRetryObserver, errors: []error{transportErr}, wantAttempts: 1, wantError: transportErr.Error()},
		{name: "other conflict fail fast", role: attachmentRetryObserver, errors: []error{protocolErr(engine.SessionErrorSessionTerminating)}, wantAttempts: 1, wantError: engine.SessionErrorSessionTerminating},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fakeNow := time.Now()
			policy := attachmentRetryPolicy{
				timeout: time.Minute, initialBackoff: time.Millisecond,
				maxBackoff: time.Second, now: func() time.Time { return fakeNow },
				wait: func(_ context.Context, delay time.Duration) error {
					fakeNow = fakeNow.Add(delay)
					return nil
				},
			}
			attempts := 0
			waits := 0
			err := retrySessionAttachment(t.Context(), test.role, func() { waits++ }, func(context.Context) error {
				idx := attempts
				attempts++
				if idx >= len(test.errors) {
					return nil
				}
				return test.errors[idx]
			}, policy)
			require.Equal(t, test.wantAttempts, attempts)
			require.Equal(t, test.wantWaits, waits)
			if test.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.wantError)
			}
		})
	}
}

func TestObserverRetryDeadVersusLiveAttachment(t *testing.T) {
	t.Parallel()

	t.Run("dead attachment clears", func(t *testing.T) {
		t.Parallel()
		occupied := true
		attempts := 0
		waits := 0
		policy := instantRetryPolicy(5 * time.Second)
		err := retrySessionAttachment(t.Context(), attachmentRetryObserver, func() { waits++ }, func(context.Context) error {
			attempts++
			if occupied {
				return &SessionProtocolError{StatusCode: http.StatusConflict, Code: engine.SessionErrorAlreadyAttached}
			}
			return nil
		}, attachmentRetryPolicy{
			timeout: policy.timeout, initialBackoff: policy.initialBackoff, maxBackoff: policy.maxBackoff,
			now: policy.now,
			wait: func(ctx context.Context, delay time.Duration) error {
				occupied = false
				return policy.wait(ctx, delay)
			},
		})
		require.NoError(t, err)
		require.Equal(t, 2, attempts)
		require.Equal(t, 1, waits)
	})

	t.Run("live attachment reaches deadline", func(t *testing.T) {
		t.Parallel()
		policy := instantRetryPolicy(5 * time.Second)
		attempts := 0
		waits := 0
		err := retrySessionAttachment(t.Context(), attachmentRetryObserver, func() { waits++ }, func(context.Context) error {
			attempts++
			return &SessionProtocolError{StatusCode: http.StatusConflict, Code: engine.SessionErrorAlreadyAttached}
		}, policy)
		require.ErrorContains(t, err, "deadline exceeded")
		require.Greater(t, attempts, 1)
		require.Equal(t, 1, waits)
	})
}

func instantRetryPolicy(timeout time.Duration) attachmentRetryPolicy {
	now := time.Now()
	return attachmentRetryPolicy{
		timeout: timeout, initialBackoff: time.Second, maxBackoff: time.Second,
		now: func() time.Time { return now },
		wait: func(context.Context, time.Duration) error {
			now = now.Add(time.Second)
			return nil
		},
	}
}

func TestAttachmentRetryReusesIdentity(t *testing.T) {
	t.Parallel()
	client := &Client{
		Params:         Params{ID: "observer", SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", SecretToken: "token"},
		connectionMode: connectionModeObserver,
		attachmentID:   "att_aaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	first := client.clientMetadata()
	for range 5 {
		next := client.clientMetadata()
		require.Equal(t, first.ClientID, next.ClientID)
		require.Equal(t, first.ClientSecretToken, next.ClientSecretToken)
		require.Equal(t, first.AttachmentID, next.AttachmentID)
	}
}

func TestClientClosePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mode     connectionMode
		wantPath string
	}{
		{name: "ordinary shutdown", mode: connectionModeOrdinary, wantPath: engine.ShutdownEndpoint},
		{name: "detachable attachment close", mode: connectionModeDetachableCreator, wantPath: engine.SessionsEndpoint + "/sess_aaaaaaaaaaaaaaaaaaaaaaaaaa/attachments/att_aaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{name: "observer attachment close", mode: connectionModeObserver, wantPath: engine.SessionsEndpoint + "/sess_aaaaaaaaaaaaaaaaaaaaaaaaaa/attachments/att_aaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{name: "control local only", mode: connectionModeControl},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var paths []string
			roundTripper := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				mu.Lock()
				paths = append(paths, req.URL.Path)
				mu.Unlock()
				return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})
			internalCtx, internalCancel := context.WithCancelCause(context.Background())
			closeCtx, closeCancel := context.WithCancelCause(context.Background())
			c := &Client{
				Params:         Params{SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", SecretToken: "token"},
				connectionMode: test.mode, attachmentID: "att_aaaaaaaaaaaaaaaaaaaaaaaaaa",
				internalCtx: internalCtx, internalCancel: internalCancel,
				closeCtx: closeCtx, closeRequests: closeCancel,
				eg:         new(errgroup.Group),
				httpClient: &httpClient{inner: &http.Client{Transport: roundTripper}},
			}
			require.NoError(t, c.Close())
			mu.Lock()
			defer mu.Unlock()
			if test.wantPath == "" {
				require.Empty(t, paths)
			} else {
				require.Equal(t, []string{test.wantPath}, paths)
			}
		})
	}
}

func TestClientTerminateUsesSessionDelete(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var requests []string
	roundTripper := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, req.Method+" "+req.URL.Path)
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusNoContent, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader("")), Request: req,
		}, nil
	})
	internalCtx, internalCancel := context.WithCancelCause(context.Background())
	closeCtx, closeCancel := context.WithCancelCause(context.Background())
	client := &Client{
		Params:         Params{SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa", SecretToken: "token"},
		connectionMode: connectionModeDetachableCreator,
		attachmentID:   "att_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		internalCtx:    internalCtx,
		internalCancel: internalCancel,
		closeCtx:       closeCtx,
		closeRequests:  closeCancel,
		eg:             new(errgroup.Group),
		httpClient:     &httpClient{inner: &http.Client{Transport: roundTripper}},
	}

	require.NoError(t, client.Terminate(t.Context()))
	require.Empty(t, client.attachmentID)
	mu.Lock()
	require.Equal(t, []string{"DELETE /v1/sessions/sess_aaaaaaaaaaaaaaaaaaaaaaaaaa"}, requests)
	mu.Unlock()
}

func TestAttachmentCloseTreatsCodedNotFoundAsIdempotent(t *testing.T) {
	t.Parallel()
	attempts := 0
	client := &Client{
		Params:       Params{SessionID: "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa"},
		attachmentID: "att_aaaaaaaaaaaaaaaaaaaaaaaaaa",
		httpClient: &httpClient{inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"error":{"code":"session_not_found","message":"gone"}}`,
				)),
				Request: req,
			}, nil
		})}},
	}

	require.NoError(t, client.CloseAttachment(t.Context()))
	require.Empty(t, client.attachmentID)
	require.NoError(t, client.CloseAttachment(t.Context()))
	require.Equal(t, 1, attempts)
}

func TestSessionJSONReportsIncompatibleEngine(t *testing.T) {
	t.Parallel()
	client := &Client{httpClient: &httpClient{inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": {"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("404 page not found")),
			Request:    req,
		}, nil
	})}}}

	err := client.sessionJSON(t.Context(), http.MethodGet, engine.SessionsEndpoint, nil, http.StatusOK, nil)
	require.ErrorIs(t, err, ErrDetachableSessionsUnsupported)
	require.ErrorContains(t, err, "upgrade the engine")
}

func TestSessionJSONPreservesCodedNotFound(t *testing.T) {
	t.Parallel()
	client := &Client{httpClient: &httpClient{inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"error":{"code":"session_not_found","message":"session missing"}}`,
			)),
			Request: req,
		}, nil
	})}}}

	err := client.sessionJSON(t.Context(), http.MethodGet, engine.SessionsEndpoint, nil, http.StatusOK, nil)
	require.NotErrorIs(t, err, ErrDetachableSessionsUnsupported)
	var protocolErr *SessionProtocolError
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, engine.SessionErrorSessionNotFound, protocolErr.Code)
}

func TestConnectSessionAttachablesDecodesCodedConflict(t *testing.T) {
	t.Parallel()
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})
	serverErr := make(chan error, 1)
	go func() {
		req, err := http.ReadRequest(bufio.NewReader(serverConn))
		if err != nil {
			serverErr <- err
			return
		}
		_ = req.Body.Close()
		body := `{"error":{"code":"attachment_connection_exists","message":"stale registration"}}`
		resp := &http.Response{
			StatusCode:    http.StatusConflict,
			Status:        "409 Conflict",
			Header:        http.Header{"Content-Type": {"application/json"}},
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
		}
		serverErr <- resp.Write(serverConn)
	}()

	sessionSrv, err := ConnectSessionAttachables(t.Context(), clientConn, http.Header{})
	require.Nil(t, sessionSrv)
	var protocolErr *SessionProtocolError
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, http.StatusConflict, protocolErr.StatusCode)
	require.Equal(t, engine.SessionErrorAttachmentConnectionExists, protocolErr.Code)
	require.Equal(t, "stale registration", protocolErr.Message)
	require.NoError(t, <-serverErr)
}

func TestDetachableTelemetryConsumersStopWithLocalAttachment(t *testing.T) {
	t.Parallel()
	for _, mode := range []connectionMode{connectionModeDetachableCreator, connectionModeObserver} {
		closeCtx, closeRequests := context.WithCancelCause(context.Background())
		client := &Client{connectionMode: mode, closeCtx: closeCtx, closeRequests: closeRequests}
		consumerCtx := client.telemetryConsumerContext(context.Background())
		closeRequests(errors.New("attachment closed"))
		select {
		case <-consumerCtx.Done():
			require.ErrorContains(t, context.Cause(consumerCtx), "attachment closed")
		case <-time.After(time.Second):
			t.Fatalf("connection mode %d telemetry consumer did not stop", mode)
		}
	}
}

func TestOrdinaryTelemetryConsumerIgnoresLocalRequestCancellation(t *testing.T) {
	t.Parallel()
	closeCtx, closeRequests := context.WithCancelCause(context.Background())
	client := &Client{
		connectionMode: connectionModeOrdinary,
		closeCtx:       closeCtx,
		closeRequests:  closeRequests,
	}
	consumerCtx := client.telemetryConsumerContext(context.Background())
	closeRequests(errors.New("ordinary close begins"))
	select {
	case <-consumerCtx.Done():
		t.Fatal("ordinary telemetry consumer stopped before server shutdown drain")
	default:
	}
}

func TestOTLPConsumerCancellationClosesSSEConnection(t *testing.T) {
	t.Parallel()
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	httpClient := &httpClient{inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       reader,
			Request:    req,
		}, nil
	})}}
	group := new(errgroup.Group)
	consumer := &otlpConsumer{httpClient: httpClient, path: "/events", eg: group}
	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, consumer.Consume(ctx, func([]byte) error { return nil }))
	cancel()
	done := make(chan error, 1)
	go func() { done <- group.Wait() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("SSE consumer did not stop after local cancellation")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
