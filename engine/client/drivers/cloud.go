package drivers

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/internal/cloud"
)

var (
	TLSHandshakeTimeout = 15 * time.Second
)

func init() {
	register("dagger-cloud", &daggerCloudDriver{})
}

// daggerCloudDriver creates and manages a Cloud Engine, then connects to it
type daggerCloudDriver struct{}

type daggerCloudConnector struct {
	EngineSpec cloud.EngineSpec
}

func (dc *daggerCloudConnector) Connect(ctx context.Context) (net.Conn, error) {
	serverAddr := dc.EngineSpec.URL

	// Extract hostname for SNI
	host, _, err := net.SplitHostPort(serverAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to split host and port from '%s': %w", serverAddr, err)
	}

	clientCert, err := dc.EngineSpec.TLSCertificate()
	if err != nil {
		return nil, fmt.Errorf("failed to unserialize Cloud Engine cert: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*clientCert},
		ServerName:   host,             // Required for SNI and server certificate validation
		MinVersion:   tls.VersionTLS12, // TLS 1.2 is the oldest version that is still maintained in 2025
	}

	dialer := net.Dialer{Timeout: 10 * time.Second}
	rawConn, err := dialer.DialContext(ctx, "tcp", serverAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial TCP to '%s': %w", serverAddr, err)
	}

	// Create TLS Client Connection
	// tls.Client wraps the rawConn. If Handshake fails, rawConn should be closed.
	// If Handshake succeeds, closing tlsConn will close rawConn.
	tlsConn := tls.Client(rawConn, tlsConfig)

	// Use HandshakeContext for better cancellation and timeout control
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, TLSHandshakeTimeout)
	defer cancelHandshake()

	if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
		rawConn.Close() // Ensure raw connection is closed if handshake fails
		return nil, fmt.Errorf("tls handshake failed with '%s': %w", serverAddr, err)
	}

	return tlsConn, nil
}

func (d *daggerCloudDriver) Available(ctx context.Context) (bool, error) {
	return true, nil // assume always available
}

func (d *daggerCloudDriver) Provision(ctx context.Context, _ *url.URL, opts *DriverOpts) (Connector, error) {
	client, err := cloud.NewClient(ctx)
	if err != nil {
		return nil, errors.New("please run `dagger login <org>` first or configure a DAGGER_CLOUD_TOKEN")
	}

	var (
		module, function string
		execCmd          []string
	)
	if opts != nil {
		module = opts.Module
		function = opts.Function
		execCmd = opts.ExecCmd
	}

	engineSpec, err := client.Engine(ctx, cloud.EngineRequest{Module: module, Function: function, ExecCmd: execCmd, ClientID: opts.ClientID})
	if err != nil {
		if errors.Is(err, cloud.ErrNoOrg) {
			return nil, errors.New("please associate this Engine with an org by running `dagger login <org>")
		}
		return nil, err
	}

	return &daggerCloudConnector{EngineSpec: *engineSpec}, nil
}

func (d *daggerCloudDriver) ImageLoader(ctx context.Context) imageload.Backend {
	return nil
}
