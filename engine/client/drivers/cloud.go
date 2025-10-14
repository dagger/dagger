package drivers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud"
)

var (
	TLSHandshakeTimeout = 15 * time.Second
)

func init() {
	register("dagger-cloud", &DaggerCloudDriver{})
}

// DaggerCloudDriver creates and manages a Cloud Engine, then connects to it
type DaggerCloudDriver struct{}

type DaggerCloudConnector struct {
	EngineSpec cloud.EngineSpec
}

func (dc *DaggerCloudConnector) Connect(ctx context.Context) (net.Conn, error) {
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

func (d *DaggerCloudDriver) Available(ctx context.Context) (bool, error) {
	return true, nil // assume always available
}

func (d *DaggerCloudDriver) Provision(ctx context.Context, _ *url.URL, opts *DriverOpts) (Connector, error) {
	client, err := cloud.NewClient(ctx, opts.CloudToken, opts.CloudBasicToken)
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

	engineSpec, err := client.Engine(ctx, cloud.EngineRequest{
		Module:   module,
		Function: function,
		ExecCmd:  execCmd,
		OrgID:    opts.CloudOrgID,
	})
	if err != nil {
		if errors.Is(err, cloud.ErrNoOrg) {
			return nil, errors.New("please associate this Engine with an org by running `dagger login <org>")
		}
		return nil, fmt.Errorf("failed to provision Cloud Engine: %w", err)
	}

	// TODO:
	// TODO:
	// TODO:
	// TODO:
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)
	bs, err := json.Marshal(engineSpec)
	if err != nil {
		slog.Error("failed to marshal engine spec for logging", "error", err)
	} else {
		slog.Warn("provisioned cloud engine", "engine_spec", string(bs))
	}

	return &DaggerCloudConnector{EngineSpec: *engineSpec}, nil
}

func (d *DaggerCloudDriver) ImageLoader(ctx context.Context) imageload.Backend {
	return nil
}
