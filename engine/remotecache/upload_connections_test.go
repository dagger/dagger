package remotecache

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The engine's PUTs never share a connection: a blob store's flow-control
// window per connection caps a shared connection at a window per round trip,
// whatever the uploads in flight. The blob store here speaks HTTP/2. Once
// the client has a connection, as a running engine's has, the uploads in
// flight together still each take a connection of their own, over HTTP/1.1,
// asked for in the TLS handshake, and the next uploads reuse them.
func TestUploadsTakeAConnectionEach(t *testing.T) {
	t.Parallel()
	const n = UploadConcurrency
	var (
		mu      sync.Mutex
		offers  [][]string
		dialled int
		// A round's uploads each wait in the handler until all want of
		// them have arrived, so they are in flight together.
		want, arrived int
		all           chan struct{}
		addrs         map[string]bool
		protos        []int
	)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		addrs[r.RemoteAddr] = true
		protos = append(protos, r.ProtoMajor)
		arrived++
		if arrived == want {
			close(all)
		}
		round := all
		mu.Unlock()
		select {
		case <-round:
		case <-time.After(testUploadGuard):
		}
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	srv.EnableHTTP2 = true
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			mu.Lock()
			dialled++
			mu.Unlock()
		}
	}
	srv.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		mu.Lock()
		offers = append(offers, hello.SupportedProtos)
		mu.Unlock()
		return nil, nil
	}}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	client := uploadClient(testUploadPatient)
	transport := client.Transport.(*http.Transport)
	// The test server's root, in the client's own TLS configuration.
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	cfg := transport.TLSClientConfig.Clone()
	if cfg == nil {
		cfg = &tls.Config{}
	}
	cfg.RootCAs = roots
	transport.TLSClientConfig = cfg
	t.Cleanup(transport.CloseIdleConnections)
	put := httpPut(client, testUploadPatient)

	// upload makes k uploads at once, and returns how many connections they
	// used and the HTTP versions they spoke.
	upload := func(k int) (int, []int) {
		mu.Lock()
		want, arrived, all, addrs, protos = k, 0, make(chan struct{}), map[string]bool{}, nil
		mu.Unlock()
		var wg sync.WaitGroup
		errs := make([]error, k)
		for i := range k {
			wg.Go(func() {
				body := bytes.NewReader(make([]byte, 64<<10))
				errs[i] = putWithin(t, put, srv.URL+"/put", body, body.Size())
			})
		}
		wg.Wait()
		for _, err := range errs {
			require.NoError(t, err)
		}
		mu.Lock()
		defer mu.Unlock()
		return len(addrs), protos
	}

	upload(1)
	conns, protos := upload(n)
	require.Equal(t, n, conns, "connections used by %d uploads in flight together", n)
	for _, p := range protos {
		require.Equal(t, 1, p, "the HTTP version an upload spoke")
	}
	conns, _ = upload(n)
	require.Equal(t, n, conns, "connections used by the next %d uploads", n)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, n, dialled, "connections dialled in all, the next uploads reusing the first's")
	for _, offer := range offers {
		require.Equal(t, []string{"http/1.1"}, offer, "the protocols an upload asked for")
	}
}
