package client

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

// shutdownTestClient returns a client whose connections to the engine come
// from dial.
func shutdownTestClient(dial func(ctx context.Context) (net.Conn, error)) *Client {
	return &Client{
		Params:      Params{SecretToken: "secret"},
		internalCtx: context.Background(),
		httpClient: &httpClient{
			inner: &http.Client{
				Transport: &http2.Transport{
					AllowHTTP: true,
					DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
						return dial(ctx)
					},
				},
			},
			headers:     http.Header{},
			secretToken: "secret",
		},
	}
}

func TestShutdownErrorSaysWhenNoConnectionWasMade(t *testing.T) {
	t.Setenv(shutdownTimeoutEnvName, "100ms")

	client := shutdownTestClient(func(ctx context.Context) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	err := client.shutdownServer()
	require.ErrorContains(t, err, "no connection to the engine after")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestShutdownErrorSaysWhenTheEngineDidNotAnswer(t *testing.T) {
	t.Setenv(shutdownTimeoutEnvName, "100ms")

	engineDone := make(chan struct{})
	t.Cleanup(func() { close(engineDone) })
	client := shutdownTestClient(func(context.Context) (net.Conn, error) {
		clientConn, engineConn := net.Pipe()
		t.Cleanup(func() {
			clientConn.Close()
			engineConn.Close()
		})
		go (&http2.Server{}).ServeConn(engineConn, &http2.ServeConnOpts{
			Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				<-engineDone
			}),
		})
		return clientConn, nil
	})

	err := client.shutdownServer()
	require.ErrorContains(t, err, "new connection after")
	require.ErrorContains(t, err, "request sent after")
	require.ErrorContains(t, err, "no response after")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestShutdownErrorDescribesTheLastAttempt(t *testing.T) {
	t.Setenv(shutdownTimeoutEnvName, "100ms")

	// The first connection's engine sends the request away with a GOAWAY, so
	// the transport retries on a new connection, which never comes.
	var dials atomic.Int32
	client := shutdownTestClient(func(ctx context.Context) (net.Conn, error) {
		if dials.Add(1) > 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		clientConn, engineConn := net.Pipe()
		t.Cleanup(func() {
			clientConn.Close()
			engineConn.Close()
		})
		go func() {
			if _, err := io.ReadFull(engineConn, make([]byte, len(http2.ClientPreface))); err != nil {
				return
			}
			fr := http2.NewFramer(engineConn, engineConn)
			// The pipe is unbuffered and the client writes while it reads, so
			// write from another goroutine and never stop reading.
			gotHeaders := make(chan struct{})
			go func() {
				if err := fr.WriteSettings(); err != nil {
					return
				}
				<-gotHeaders
				_ = fr.WriteGoAway(0, http2.ErrCodeNo, nil)
			}()
			for {
				f, err := fr.ReadFrame()
				if err != nil {
					return
				}
				if _, ok := f.(*http2.HeadersFrame); ok {
					close(gotHeaders)
				}
			}
		}()
		return clientConn, nil
	})

	err := client.shutdownServer()
	require.ErrorContains(t, err, "connection attempt 2: no connection to the engine after")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
