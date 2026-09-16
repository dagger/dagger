package realm

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTransportSeparatesRealms(t *testing.T) {
	var connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	t.Cleanup(server.Close)

	transport := NewTransport(http.DefaultTransport.(*http.Transport))
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}

	request := func(realm Realm) {
		req, err := http.NewRequestWithContext(
			With(context.Background(), realm),
			http.MethodGet,
			server.URL,
			nil,
		)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	request(Userland)
	request(Userland)
	request(Daggerland)
	request(Daggerland)
	require.EqualValues(t, 2, connections.Load())
}

func TestTransportRequiresRealm(t *testing.T) {
	transport := NewTransport(http.DefaultTransport.(*http.Transport))
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.ErrorIs(t, err, ErrRequired)
}

func TestWithDefaultPreservesRealm(t *testing.T) {
	ctx := With(context.Background(), Daggerland)
	ctx = WithDefault(ctx, Userland)
	require.Equal(t, Daggerland, FromContext(ctx))

	ctx = WithDefault(context.Background(), Userland)
	require.Equal(t, Userland, FromContext(ctx))
}

func TestDialerRequiresRealm(t *testing.T) {
	_, err := Unattributed.Dialer(net.Dialer{}).
		DialContext(context.Background(), "tcp", "127.0.0.1:1")
	require.ErrorIs(t, err, ErrRequired)
}

func TestListenRequiresRealm(t *testing.T) {
	_, err := Unattributed.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.ErrorIs(t, err, ErrRequired)
}
