package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// The fake engine performs the real attachables upgrade, then calls the real
// registered gRPC server over that connection. No Dagger engine is required.
func newAttachablesLifetimeClient(t *testing.T, e2e bool) (*Client, context.CancelFunc, grpc_health_v1.HealthClient, <-chan struct{}) {
	t.Helper()
	commandCtx, cancelCommand := context.WithCancel(context.Background())
	t.Cleanup(cancelCommand)
	internalCtx, cancelInternal := context.WithCancelCause(context.WithoutCancel(commandCtx))
	closeCtx, closeRequests := context.WithCancelCause(context.WithoutCancel(commandCtx))
	t.Cleanup(func() { cancelInternal(context.Canceled); closeRequests(context.Canceled) })
	c := &Client{Params: Params{ID: "lifetime-test", SessionID: "lifetime-test"},
		internalCtx: internalCtx, internalCancel: cancelInternal,
		closeCtx: closeCtx, closeRequests: closeRequests}
	c.eg, c.internalCtx = errgroup.WithContext(c.internalCtx)
	clientConn, engineConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close(); _ = engineConn.Close() })
	observed := &observedAttachablesConn{Conn: clientConn, closed: make(chan struct{})}
	c.connector = &attachablesPipeConnector{conn: observed}
	type engineResult struct {
		conn *grpc.ClientConn
		err  error
	}
	engineReady := make(chan engineResult, 1)
	go func() {
		req, err := http.ReadRequest(bufio.NewReader(engineConn))
		if err != nil {
			engineReady <- engineResult{err: err}
			return
		}
		if req.URL.Path != engine.SessionAttachablesEndpoint {
			engineReady <- engineResult{err: fmt.Errorf("unexpected path %s", req.URL.Path)}
			return
		}
		response := &http.Response{StatusCode: http.StatusSwitchingProtocols, Header: http.Header{
			"Connection": {"Upgrade"}, "Upgrade": {"h2c"},
		}}
		if err := response.Write(engineConn); err != nil {
			engineReady <- engineResult{err: err}
			return
		}
		if _, err := io.ReadFull(engineConn, make([]byte, 1)); err != nil {
			engineReady <- engineResult{err: err}
			return
		}
		var mu sync.Mutex
		dialed := false
		peer, err := grpc.NewClient("passthrough:///attachables-lifetime-test",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
				mu.Lock()
				defer mu.Unlock()
				if dialed {
					return nil, errors.New("test attachables connection already used")
				}
				dialed = true
				return engineConn, nil
			}))
		engineReady <- engineResult{conn: peer, err: err}
	}()
	if e2e {
		// These proxies are registered by the real E2E path, but this test calls
		// its own standard health service; it needs no upstream engine.
		caller, err := grpc.NewClient("passthrough:///unused-upstream",
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		t.Cleanup(func() { _ = caller.Close() })
		require.NoError(t, c.startE2ESession(commandCtx, caller))
	} else {
		require.NoError(t, c.startSession(commandCtx))
	}
	var peer *grpc.ClientConn
	select {
	case result := <-engineReady:
		require.NoError(t, result.err)
		peer = result.conn
	case <-time.After(2 * time.Second):
		t.Fatal("attachables handshake did not complete")
	}
	t.Cleanup(func() { _ = peer.Close() })
	health := grpc_health_v1.NewHealthClient(peer)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := health.Check(ctx, &grpc_health_v1.HealthCheckRequest{}, grpc.WaitForReady(true))
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, response.Status)
	return c, cancelCommand, health, observed.closed
}

func TestSessionAttachablesRemainAvailableThroughCancelledCommandShutdown(t *testing.T) {
	// startSession reads ordinary local client configuration; isolate it so
	// this test neither consumes user credentials nor starts Cloud exports.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("SSH_AUTH_SOCK", "")
	for _, e2e := range []bool{false, true} {
		t.Run(fmt.Sprintf("e2e=%t", e2e), func(t *testing.T) {
			c, cancelCommand, health, attachablesClosed := newAttachablesLifetimeClient(t, e2e)
			cancelCommand()
			select {
			case <-attachablesClosed:
				t.Fatal("command cancellation closed attachables before engine shutdown")
			case <-time.After(25 * time.Millisecond):
			}
			shutdownCalled := false
			c.httpClient = &httpClient{inner: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != engine.ShutdownEndpoint {
					return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
				}
				shutdownCalled = true
				ctx, cancel := context.WithTimeout(req.Context(), time.Second)
				defer cancel()
				response, err := health.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
				if err != nil {
					return nil, fmt.Errorf("shutdown could not reach host attachables: %w", err)
				}
				if response.Status != grpc_health_v1.HealthCheckResponse_SERVING {
					return nil, fmt.Errorf("attachables not serving: %s", response.Status)
				}
				return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})}}
			require.NoError(t, c.Close())
			require.True(t, shutdownCalled)
			select {
			case <-attachablesClosed:
			case <-time.After(time.Second):
				t.Fatal("Close did not stop attachables after engine shutdown")
			}
		})
	}
}

func TestSessionAttachablesStopOnClientInitializationFailure(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	t.Setenv("SSH_AUTH_SOCK", "")
	for _, e2e := range []bool{false, true} {
		t.Run(fmt.Sprintf("e2e=%t", e2e), func(t *testing.T) {
			c, _, _, closed := newAttachablesLifetimeClient(t, e2e)
			c.internalCancel(errors.New("client initialization failed"))
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("failed initialization left attachables running")
			}
			done := make(chan error, 1)
			go func() { done <- c.eg.Wait() }()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(time.Second):
				t.Fatal("attachables lifetime goroutine did not finish")
			}
		})
	}
}

type observedAttachablesConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *observedAttachablesConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type attachablesPipeConnector struct {
	mu   sync.Mutex
	conn net.Conn
}

func (c *attachablesPipeConnector) EngineID() string { return "test" }
func (c *attachablesPipeConnector) Connect(context.Context) (net.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil, errors.New("unexpected extra test connection")
	}
	conn := c.conn
	c.conn = nil
	return conn, nil
}
