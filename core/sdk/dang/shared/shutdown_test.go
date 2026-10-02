package dangshared

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
)

// Reproduces the stray connection: request A holds the pool's only
// connection, request B starts a dial, A finishes and B takes A's connection,
// then B's dial completes with nothing to carry. That connection never sends
// a request, so the server sees it in StateNew, and http.Server.Shutdown only
// treats StateNew as idle after 5s. Draining the pool before Shutdown must
// keep the invocation well under that.
func TestServeNestedClientDoesNotWaitOnUnusedConnection(t *testing.T) {
	t.Parallel()

	releaseA := make(chan struct{})
	aInHandler := make(chan struct{})
	var aOnce sync.Once
	var served atomic.Int32
	var accepted atomic.Int32
	httpSrv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if served.Add(1) == 1 {
				aOnce.Do(func() { close(aInHandler) })
				<-releaseA
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{}}`))
		}),
		ConnState: func(c net.Conn, s http.ConnState) {
			if s == http.StateNew {
				accepted.Add(1)
			}
		},
	}

	// The second dial is held until the test lets it go, so it cannot win
	// the race against A's freed connection.
	releaseDial := make(chan struct{})
	var dials atomic.Int32
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if dials.Add(1) == 2 {
			<-releaseDial
		}
		return dial(ctx, network, addr)
	}

	start := time.Now()
	_, err := serveNestedClient(t.Context(), httpSrv, transport, func(ctx context.Context, gqlClient graphql.Client) ([]byte, error) {
		query := func() error {
			return gqlClient.MakeRequest(ctx, &graphql.Request{Query: "{}"}, &graphql.Response{Data: &struct{}{}})
		}
		aDone := make(chan error, 1)
		go func() { aDone <- query() }()
		<-aInHandler // A holds the only connection

		bDone := make(chan error, 1)
		go func() { bDone <- query() }() // B starts dial #2, which is held
		require.Eventually(t, func() bool { return dials.Load() == 2 }, 5*time.Second, time.Millisecond)

		close(releaseA) // A finishes; B reuses A's connection
		require.NoError(t, <-aDone)
		require.NoError(t, <-bDone)
		require.Equal(t, int32(2), served.Load())

		close(releaseDial) // dial #2 lands with nothing to carry
		require.Eventually(t, func() bool { return accepted.Load() == 2 }, 5*time.Second, time.Millisecond)
		return nil, nil
	})
	require.NoError(t, err)
	require.Less(t, time.Since(start), 2*time.Second, "shutdown waited on the unused connection")
	require.Equal(t, int32(2), accepted.Load(), "two connections reached the server")
	require.Equal(t, int32(2), served.Load(), "both requests went over the first one")
}

// Shutdown must still wait for a request that is in flight when fn returns.
func TestServeNestedClientDrainsInFlightRequest(t *testing.T) {
	t.Parallel()

	inHandler := make(chan struct{})
	release := make(chan struct{})
	httpSrv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(inHandler)
			<-release
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{}}`))
		}),
	}

	inflight := make(chan error, 1)
	_, err := serveNestedClient(t.Context(), httpSrv, http.DefaultTransport.(*http.Transport).Clone(), func(ctx context.Context, gqlClient graphql.Client) ([]byte, error) {
		go func() {
			inflight <- gqlClient.MakeRequest(ctx, &graphql.Request{Query: "{}"}, &graphql.Response{Data: &struct{}{}})
		}()
		<-inHandler
		go func() {
			time.Sleep(200 * time.Millisecond)
			close(release)
		}()
		return nil, nil // return while the request is still being served
	})
	require.NoError(t, err)
	require.NoError(t, <-inflight, "the in-flight request completed during shutdown")
}
