package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestClientMetadataUsesExplicitModuleInsteadOfWorkspaceModules(t *testing.T) {
	t.Parallel()

	client := &Client{
		Params: Params{
			ID:                   "client",
			SessionID:            "session",
			SecretToken:          "secret",
			Module:               "./explicit",
			LoadWorkspaceModules: true,
		},
	}

	md := client.clientMetadata()

	require.False(t, md.LoadWorkspaceModules)
	require.Equal(t, []engine.ExtraModule{{
		Ref:        "./explicit",
		Entrypoint: true,
	}}, md.ExtraModules)
}

func TestClientMetadataForwardsWorkspaceModuleScopeOnlyWithWorkspaceModules(t *testing.T) {
	t.Parallel()

	client := &Client{
		Params: Params{
			ID:                   "client",
			SessionID:            "session",
			SecretToken:          "secret",
			LoadWorkspaceModules: true,
			WorkspaceModuleScope: "good-mod",
		},
	}

	md := client.clientMetadata()
	require.True(t, md.LoadWorkspaceModules)
	require.Equal(t, "good-mod", md.WorkspaceModuleScope)

	// With an explicit -m module there are no pending workspace modules to
	// narrow, so the scope must not travel.
	client.Params.Module = "./explicit"
	md = client.clientMetadata()
	require.False(t, md.LoadWorkspaceModules)
	require.Empty(t, md.WorkspaceModuleScope)
}

// The TestClose* tests below drive the real client code: newClient, the real
// telemetry consumers (startTelemetryConsumers through go-sse), and the real
// Close. Only the engine is replaced, with a fake http.RoundTripper serving
// canned responses. The engine behavior that fake emulates — after /shutdown,
// the SSE streams drain remaining telemetry and end with a graceful EOF — is
// locked from the server side by TestTelemetrySSEDrainsAndEOFsAfterClientShutdown
// in engine/server, and against a real engine in telemetry_e2e_test.go.

// Regression test for the flake that got the telemetry split reverted: after
// shutdown, the engine is still draining telemetry to the SSE streams. Close
// must wait for that drain before tearing down local resources, otherwise a
// successful command fails with a spurious telemetry error.
func TestCloseDrainsTelemetryBeforeTeardown(t *testing.T) {
	t.Parallel()

	stream := newFakeSSEStream()
	shutdownDone := make(chan struct{})

	c := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == engine.ShutdownEndpoint {
			return &http.Response{StatusCode: http.StatusNoContent, Body: bodyCloseFunc(func() {
				close(shutdownDone)
			})}, nil
		}
		return stream.respond(req)
	})
	require.NoError(t, c.startTelemetryConsumers(context.Background(), c.httpClient))

	// Like the real engine, the stream stays open a little longer after
	// shutdown while remaining telemetry drains. A Close that tears down
	// without waiting for the drain cancels closeCtx during that window.
	var closeCtxErrDuringDrain error
	recorded := make(chan struct{})
	go func() {
		<-shutdownDone
		time.Sleep(100 * time.Millisecond)
		closeCtxErrDuringDrain = c.closeCtx.Err()
		close(recorded)
		stream.endGracefully()
	}()

	require.NoError(t, c.Close())
	<-recorded
	require.NoError(t, closeCtxErrDuringDrain, "client teardown started before telemetry drained")
}

// Draining earlier must not make telemetry best-effort: a consumer that fails
// for real — its stream breaks mid-read and the engine rejects the reconnect —
// still fails Close.
func TestCloseReturnsTelemetryConsumerError(t *testing.T) {
	t.Parallel()

	stream := newFakeSSEStream()
	var telemetryRequests atomic.Int32
	c := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == engine.ShutdownEndpoint:
			return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
		case telemetryRequests.Add(1) == 1:
			return stream.respond(req)
		default: // reject the SSE reconnect after the stream breaks
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		}
	})
	require.NoError(t, c.startTelemetryConsumers(context.Background(), c.httpClient))

	stream.breakStream()

	require.ErrorContains(t, c.Close(), "wait for telemetry")
}

// A stream that never reaches EOF must not hang or fail Close: the drain times
// out (logged, not returned) and teardown's cancellation unblocks the consumer.
func TestCloseUnblocksStuckTelemetryStream(t *testing.T) {
	t.Parallel()

	stream := newFakeSSEStream()
	c := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == engine.ShutdownEndpoint {
			return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
		}
		return stream.respond(req)
	})
	c.telemetryDrainTimeout = 50 * time.Millisecond
	require.NoError(t, c.startTelemetryConsumers(context.Background(), c.httpClient))

	start := time.Now()
	require.NoError(t, c.Close())
	require.Less(t, time.Since(start), 5*time.Second, "Close did not return promptly")

	select {
	case <-c.telemetryDone:
	case <-time.After(time.Second):
		t.Fatal("telemetry consumer did not stop after cancellation")
	}
}

// If shutdown fails the engine will never send the graceful EOF, so Close must
// not sit out the full drain timeout before tearing down.
func TestCloseSkipsDrainWhenShutdownFails(t *testing.T) {
	t.Parallel()

	stream := newFakeSSEStream()
	shutdownErr := errors.New("shutdown failed")
	c := newTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == engine.ShutdownEndpoint {
			return nil, shutdownErr
		}
		return stream.respond(req)
	})
	require.NoError(t, c.startTelemetryConsumers(context.Background(), c.httpClient))

	start := time.Now()
	require.ErrorIs(t, c.Close(), shutdownErr)
	require.Less(t, time.Since(start), defaultTelemetryDrainTimeout, "Close waited for a drain that cannot happen")
}

// newTestClient builds a Client through the real constructor, with the engine
// replaced by a fake transport.
func newTestClient(t *testing.T, rt roundTripperFunc) *Client {
	t.Helper()

	c := newClient(context.Background(), Params{
		EngineTrace: tracetest.NewNoopExporter(),
	})
	c.httpClient = &httpClient{inner: &http.Client{Transport: rt}}
	return c
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

// fakeSSEStream serves an SSE response that emits the initial "subscribed"
// event and then stays open until endGracefully (EOF, like the engine's
// post-shutdown drain), breakStream (a mid-stream read error), or
// request-context cancellation.
type fakeSSEStream struct {
	graceful chan struct{}
	broken   chan struct{}
}

func newFakeSSEStream() *fakeSSEStream {
	return &fakeSSEStream{
		graceful: make(chan struct{}),
		broken:   make(chan struct{}),
	}
}

func (s *fakeSSEStream) respond(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body: &fakeSSEBody{
			stream: s,
			ctx:    req.Context(),
			data:   strings.NewReader("event: subscribed\ndata\n\n"),
		},
	}, nil
}

func (s *fakeSSEStream) endGracefully() {
	close(s.graceful)
}

func (s *fakeSSEStream) breakStream() {
	close(s.broken)
}

type fakeSSEBody struct {
	stream *fakeSSEStream
	ctx    context.Context
	data   *strings.Reader
}

func (b *fakeSSEBody) Read(p []byte) (int, error) {
	if b.data.Len() > 0 {
		return b.data.Read(p)
	}
	select {
	case <-b.stream.graceful:
		return 0, io.EOF
	case <-b.stream.broken:
		return 0, io.ErrUnexpectedEOF
	case <-b.ctx.Done():
		// like net/http: a canceled request surfaces as a read error
		return 0, b.ctx.Err()
	}
}

func (b *fakeSSEBody) Close() error { return nil }

// bodyCloseFunc is a response body that runs fn when the client closes it.
type bodyCloseFunc func()

func (bodyCloseFunc) Read([]byte) (int, error) { return 0, io.EOF }
func (fn bodyCloseFunc) Close() error          { fn(); return nil }
