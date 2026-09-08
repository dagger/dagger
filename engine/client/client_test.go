package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestTelemetryContextUsesClientLifetime(t *testing.T) {
	t.Parallel()

	internalCtx, cancelInternal := context.WithCancelCause(context.Background())
	client := &Client{internalCtx: internalCtx}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	telemetryCtx, cancelTelemetry := client.telemetryContext(requestCtx)
	defer cancelTelemetry(context.Canceled)

	cancelRequest()
	select {
	case <-telemetryCtx.Done():
		t.Fatal("request cancellation interrupted client telemetry drain")
	case <-time.After(10 * time.Millisecond):
	}

	clientClosed := errors.New("client initialization failed")
	cancelInternal(clientClosed)
	select {
	case <-telemetryCtx.Done():
		require.ErrorIs(t, context.Cause(telemetryCtx), clientClosed)
	case <-time.After(time.Second):
		t.Fatal("client lifetime cancellation did not stop telemetry")
	}
}

func TestOTLPConsumerStopsOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reader, writer := io.Pipe()
	defer writer.Close()
	body := &observedReadCloser{
		ReadCloser:  reader,
		readStarted: make(chan struct{}),
	}
	defer body.Close()

	httpClient := &httpClient{
		inner: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       body,
					Request:    req,
				}, nil
			}),
		},
	}
	telemetry := new(errgroup.Group)
	consumer := &otlpConsumer{
		httpClient: httpClient,
		path:       "/v1/traces",
		eg:         telemetry,
	}

	require.NoError(t, consumer.Consume(ctx, func([]byte) error {
		return errors.New("unexpected telemetry event")
	}))

	select {
	case <-body.readStarted:
	case <-time.After(time.Second):
		t.Fatal("telemetry consumer did not start reading")
	}

	cancel()
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- telemetry.Wait()
	}()

	select {
	case err := <-waitDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("telemetry consumer did not stop after context cancellation")
	}
}

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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type observedReadCloser struct {
	io.ReadCloser
	readStarted chan struct{}
	readOnce    sync.Once
}

func (body *observedReadCloser) Read(p []byte) (int, error) {
	body.readOnce.Do(func() {
		close(body.readStarted)
	})
	return body.ReadCloser.Read(p)
}
