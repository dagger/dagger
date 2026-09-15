package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
)

// setupLocalTunnel opens a loopback listener in the engine process, sets
// endpoint.dial to connect through it, and forwards every accepted connection
// to the endpoint's original host:port through the client's session, so the
// engine can reach a model server that only the client can (e.g. Ollama on
// the client's loopback). endpoint.BaseURL is left untouched so TLS
// verification/SNI and the HTTP Host header keep using the original host.
//
// The tunnel must stay alive for as long as the endpoint is in use, not just
// for the call that created it: ctx must be scoped to the client's session,
// and the tunnel shuts down when it is canceled.
func setupLocalTunnel(ctx context.Context, endpoint *LLMEndpoint) error {
	u, err := url.Parse(endpoint.BaseURL)
	if err != nil {
		return fmt.Errorf("parse local endpoint URL: %w", err)
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		default:
			port = "80"
		}
	}
	backendPort, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("parse port %q from %s: %w", port, endpoint.BaseURL, err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("get client metadata: %w", err)
	}

	// A host_ip socket bound to the client: its session dials host:port on our
	// behalf, from the client's network vantage point.
	sock := &Socket{
		Kind:           SocketKindHostIP,
		SourceClientID: clientMetadata.ClientID,
		URLVal: (&url.URL{
			Scheme: NetworkProtocolTCP.Network(),
			Host:   net.JoinHostPort(host, port),
		}).String(),
		PortForwardVal: PortForward{
			Backend:  backendPort,
			Protocol: NetworkProtocolTCP,
		},
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for local tunnel: %w", err)
	}
	tunnelAddr := listener.Addr().String()
	slog.Info("local LLM tunnel listening", "addr", tunnelAddr, "target", endpoint.BaseURL)

	go runLocalTunnel(ctx, listener, sock)

	// Route the endpoint's connections through the tunnel. The request URL
	// stays the original one, so certificates and virtual-hosted endpoints
	// keep working.
	var dialer net.Dialer
	endpoint.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, tunnelAddr)
	}

	return nil
}

// runLocalTunnel accepts connections on the listener and forwards each one to
// the target host through the client's session, until the context is canceled
// or the listener is closed.
func runLocalTunnel(ctx context.Context, listener net.Listener, sock *Socket) {
	var wg sync.WaitGroup
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				slog.Warn("local LLM tunnel accept error", "error", err)
			}
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			upstream, err := sock.ForwardAgentClient(ctx)
			if err != nil {
				slog.Warn("local LLM tunnel connect error", "error", err)
				return
			}
			if err := sshforward.Copy(ctx, conn, upstream, upstream.CloseSend); err != nil {
				slog.Warn("local LLM tunnel copy error", "error", err)
			}
		}()
	}
	wg.Wait()
}
