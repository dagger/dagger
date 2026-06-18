package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/stretchr/testify/require"
	"github.com/vito/go-sse/sse"
)

// Locks the server-side contract that the client's telemetry drain relies on:
// after a client shuts down, the SSE handler drains any queued telemetry and
// then ends the stream with a graceful EOF.
func TestTelemetrySSEDrainsAndEOFsAfterClientShutdown(t *testing.T) {
	engineSrv := &Server{
		clientDBs: clientdb.NewDBs(t.TempDir()),
	}
	pubsub := NewPubSub(engineSrv)
	client := &daggerClient{
		daggerSession: &daggerSession{
			telemetryPubSub: pubsub,
		},
		clientID:   "client",
		shutdownCh: make(chan struct{}),
	}

	var sentData bool
	handlerErr := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerErr <- pubsub.sseHandler(w, r, client, func(context.Context, *clientdb.DB, string) (*sse.Event, bool, error) {
			if sentData {
				return nil, false, nil
			}
			sentData = true
			return &sse.Event{
				ID:   "1",
				Name: "telemetry",
				Data: []byte("drained-before-shutdown"),
			}, true, nil
		})
	}))
	defer srv.Close()

	source, err := sse.Connect(http.DefaultClient, time.Millisecond, func() *http.Request {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/traces", nil)
		require.NoError(t, err, "new request")
		return req
	})
	require.NoError(t, err, "connect to SSE")
	defer source.Close()

	event, err := source.Next()
	require.NoError(t, err, "read subscribed event")
	require.Equal(t, "subscribed", event.Name)

	close(client.shutdownCh)

	event, err = source.Next()
	require.NoError(t, err, "read drained telemetry event")
	require.Equal(t, "telemetry", event.Name)
	require.Equal(t, "drained-before-shutdown", string(event.Data))

	_, err = source.Next()
	require.ErrorIs(t, err, io.EOF, "expected graceful EOF after shutdown drain")

	select {
	case err := <-handlerErr:
		require.NoError(t, err, "handler returned error")
	case <-time.After(time.Second):
		t.Fatal("handler did not return after EOF")
	}
}
