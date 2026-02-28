package drivers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/dagger/dagger/internal/buildkit/client/connhelper"
)

const (
	TLSDialTimeout = 10 * time.Second
)

func TLSConnect(u *url.URL) (*connhelper.ConnectionHelper, error) {
	host := u.Hostname()
	port := u.Port()

	if host == "" {
		return nil, fmt.Errorf("TLS driver requires a host in the URL")
	}

	if port == "" {
		port = "443"
	}

	tlsConfig, err := buildTLSConfig(u)
	if err != nil {
		return nil, fmt.Errorf("failed to build TLS config: %w", err)
	}

	serverAddr := net.JoinHostPort(host, port)

	return &connhelper.ConnectionHelper{
		ContextDialer: func(ctx context.Context, addr string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: TLSDialTimeout}
			rawConn, err := dialer.DialContext(ctx, "tcp", serverAddr)
			if err != nil {
				return nil, fmt.Errorf("failed to dial TCP to %s: %w", serverAddr, err)
			}

			tlsConn := tls.Client(rawConn, tlsConfig)

			handshakeCtx, cancel := context.WithTimeout(ctx, TLSHandshakeTimeout)
			defer cancel()

			if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
				rawConn.Close()
				return nil, fmt.Errorf("TLS handshake failed with %s: %w", serverAddr, err)
			}

			return tlsConn, nil
		},
	}, nil

}

func buildTLSConfig(target *url.URL) (*tls.Config, error) {
	query := target.Query()

	certPath := query.Get("cert_path")
	keyPath := query.Get("key_path")
	caPath := query.Get("ca_path")
	skipVerify := query.Get("skip_verify") == "true"

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: skipVerify,
	}

	if target.Hostname() != "" {
		tlsConfig.ServerName = target.Hostname()
	}

	if certPath != "" && keyPath != "" {
		certPEM, err := os.ReadFile(certPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read client certificate from %s: %w", certPath, err)
		}

		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read client key from %s: %w", keyPath, err)
		}

		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}

		tlsConfig.Certificates = []tls.Certificate{cert}
	} else if certPath != "" || keyPath != "" {
		return nil, fmt.Errorf("both cert_path and key_path must be provided for client authentication")
	}

	if caPath != "" {
		caCert, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate from %s: %w", caPath, err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", caPath)
		}

		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}
