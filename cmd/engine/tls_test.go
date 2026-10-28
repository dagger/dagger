package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	bkconfig "github.com/dagger/dagger/internal/buildkit/cmd/buildkitd/config"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

// TestTLSListenerServesHTTP2 covers the protocols clients speak to an engine
// started with --tlscert/--tlskey/--tlscacert.
func TestTLSListenerServesHTTP2(t *testing.T) {
	t.Parallel()
	pki := newTestPKI(t)

	serverTLS, err := serverCredentials(bkconfig.TLSConfig{
		Cert: pki.serverCert,
		Key:  pki.serverKey,
		CA:   pki.caCert,
	})
	require.NoError(t, err)
	l, err := getListener("tcp://127.0.0.1:0", 0, 0, serverTLS)
	require.NoError(t, err)
	srv := newAPIServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Proto)
	}))
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	addr := l.Addr().String()

	clientTLS := func(nextProtos ...string) *tls.Config {
		return &tls.Config{
			ServerName:   "engine",
			RootCAs:      pki.caPool,
			Certificates: []tls.Certificate{pki.client},
			NextProtos:   nextProtos,
		}
	}

	// get sends a request the way the dagger CLI does: prior-knowledge HTTP/2
	// over a connection its driver already set up.
	get := func(t *testing.T, cfg *tls.Config) (proto string, negotiated string, err error) {
		t.Helper()
		client := &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http2.Transport{
				AllowHTTP: true,
				DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
					conn, err := (&tls.Dialer{Config: cfg}).DialContext(ctx, "tcp", addr)
					if err != nil {
						return nil, err
					}
					negotiated = conn.(*tls.Conn).ConnectionState().NegotiatedProtocol
					return conn, nil
				},
			},
		}
		resp, err := client.Get("http://dagger/")
		if err != nil {
			return "", negotiated, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return string(body), negotiated, err
	}

	t.Run("prior-knowledge h2 without ALPN", func(t *testing.T) {
		// Every tls:// driver released up to v1.0.0-beta.21 connects like this.
		proto, negotiated, err := get(t, clientTLS())
		require.NoError(t, err)
		require.Equal(t, "", negotiated)
		require.Equal(t, "HTTP/2.0", proto)
	})

	t.Run("h2 negotiated with ALPN", func(t *testing.T) {
		proto, negotiated, err := get(t, clientTLS("h2"))
		require.NoError(t, err)
		require.Equal(t, "h2", negotiated)
		require.Equal(t, "HTTP/2.0", proto)
	})

	for _, alpn := range []string{"", "http/1.1"} {
		// The dagger CLI connects session attachables with HTTP/1.1 and no ALPN.
		t.Run("HTTP/1.1 with ALPN "+strconv.Quote(alpn), func(t *testing.T) {
			cfg := clientTLS()
			if alpn != "" {
				cfg.NextProtos = []string{alpn}
			}
			client := &http.Client{
				Timeout:   10 * time.Second,
				Transport: &http.Transport{TLSClientConfig: cfg},
			}
			resp, err := client.Get("https://" + addr + "/")
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, alpn, resp.TLS.NegotiatedProtocol)
			require.Equal(t, "HTTP/1.1", string(body))
		})
	}

	t.Run("client without certificate is rejected", func(t *testing.T) {
		cfg := clientTLS("h2")
		cfg.Certificates = nil
		// With TLS 1.3 the client may see the server's alert or a broken pipe.
		_, _, err := get(t, cfg)
		require.Error(t, err)
	})
}

type testPKI struct {
	caPool                *x509.CertPool
	caCert                string
	serverCert, serverKey string
	client                tls.Certificate
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	dir := t.TempDir()
	write := func(name, blockType string, der []byte) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600))
		return path
	}

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	issue := func(serial int64, cn string, usage x509.ExtKeyUsage, dnsNames ...string) ([]byte, *ecdsa.PrivateKey) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			DNSNames:     dnsNames,
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		}, ca, &key.PublicKey, caKey)
		require.NoError(t, err)
		return der, key
	}
	marshal := func(key *ecdsa.PrivateKey) []byte {
		der, err := x509.MarshalECPrivateKey(key)
		require.NoError(t, err)
		return der
	}

	serverDER, serverKey := issue(2, "engine", x509.ExtKeyUsageServerAuth, "engine")
	clientDER, clientKey := issue(3, "client", x509.ExtKeyUsageClientAuth)

	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return testPKI{
		caPool:     pool,
		caCert:     write("ca.crt", "CERTIFICATE", caDER),
		serverCert: write("server.crt", "CERTIFICATE", serverDER),
		serverKey:  write("server.key", "EC PRIVATE KEY", marshal(serverKey)),
		client: tls.Certificate{
			Certificate: [][]byte{clientDER},
			PrivateKey:  clientKey,
		},
	}
}
