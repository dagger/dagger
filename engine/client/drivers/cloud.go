package drivers

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/internal/cloud"
)

var TLSHandshakeTimeout = 15 * time.Second

func init() {
	register("dagger-cloud", &daggerCloudDriver{})
}

// daggerCloudDriver creates and manages a Cloud Engine, then connects to it
type daggerCloudDriver struct{}

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

func (dc *DaggerCloudConnector) EngineID() string {
	return dc.EngineSpec.InstanceID
}

func (d *daggerCloudDriver) Available(ctx context.Context) (bool, error) {
	return true, nil // assume always available
}

func (d *daggerCloudDriver) Provision(ctx context.Context, _ *url.URL, opts *DriverOpts) (Connector, error) {
	if opts.CloudAuth == nil {
		return nil, errors.New("please run `dagger login <org>` first or configure a DAGGER_CLOUD_TOKEN")
	}
	client, err := cloud.NewClient(ctx, opts.CloudAuth)
	if err != nil {
		return nil, err
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

	engineSpec, err := provisionEngine(ctx, client, cloud.EngineRequest{
		Module:   module,
		Function: function,
		ExecCmd:  execCmd,
		ClientID: opts.ClientID,
	})
	if err != nil {
		if errors.Is(err, cloud.ErrNoOrg) {
			return nil, errors.New("please associate this Engine with an org by running `dagger login <org>")
		}
		return nil, err
	}

	return &DaggerCloudConnector{EngineSpec: *engineSpec}, nil
}

// maxProvisionServerErrorRetries bounds retries on 500s, which may be
// transient (e.g. cold-start races provisioning the org's very first
// engines) but can also be persistent failures not worth hammering on.
const maxProvisionServerErrorRetries = 3

// provisionEngine requests a remote engine from the cloud API, retrying
// while the API reports transient conditions. Capacity exhaustion (429) is
// expected when many concurrent requests race for slots — e.g. a large
// `dagger check` scale-out — and is retried for as long as ctx allows, since
// slots free up as other clients finish. The overall retry budget is bounded
// by ctx, which the caller caps (10 minutes by default).
func provisionEngine(ctx context.Context, client *cloud.Client, req cloud.EngineRequest) (*cloud.EngineSpec, error) {
	backoff := time.Second
	const maxBackoff = 15 * time.Second
	serverErrRetries := 0
	for {
		spec, err := client.Engine(ctx, req)
		if err == nil {
			return spec, nil
		}

		var provisionErr *cloud.EngineProvisionError
		if !errors.As(err, &provisionErr) {
			return nil, err
		}
		retryable := provisionErr.Retryable()
		if !retryable && provisionErr.StatusCode == http.StatusInternalServerError &&
			serverErrRetries < maxProvisionServerErrorRetries {
			serverErrRetries++
			retryable = true
		}
		if !retryable {
			return nil, err
		}

		// full jitter up to half the backoff to spread out racing clients
		wait := backoff + rand.N(backoff/2) //nolint:gosec
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (gave up retrying: %w)", err, context.Cause(ctx))
		case <-time.After(wait):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (d *daggerCloudDriver) ImageLoader(ctx context.Context) imageload.Backend {
	return nil
}
