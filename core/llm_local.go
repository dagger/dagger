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
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
)

// setupLocalTunnel sets up a c2h (container-to-host) tunnel for a local LLM
// endpoint. The tunnel forwards connections from a local listener in the
// engine process through the client's SSH session to the target host.
//
// It rewrites endpoint.BaseURL to point at the local tunnel listener and
// rebuilds the LLM client so it uses the tunneled address.
func setupLocalTunnel(ctx context.Context, query *Query, endpoint *LLMEndpoint) error {
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

	// Register an IP socket for the target host:port so we can tunnel
	// through the client's SSH session.
	socketStore, err := query.Sockets(ctx)
	if err != nil {
		return fmt.Errorf("get socket store: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("get client metadata: %w", err)
	}

	pf := PortForward{
		Backend:  backendPort,
		Protocol: NetworkProtocolTCP,
	}

	accessor, err := GetHostIPSocketAccessor(ctx, query, host, pf)
	if err != nil {
		return fmt.Errorf("get host IP socket accessor: %w", err)
	}

	sock := &Socket{IDDigest: hashutil.HashStrings(accessor)}
	if err := socketStore.AddIPSocket(sock, clientMetadata.ClientID, host, pf); err != nil {
		return fmt.Errorf("register IP socket: %w", err)
	}

	// Create a local TCP listener that forwards connections through the SSH
	// session to the target host.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for local tunnel: %w", err)
	}

	tunnelAddr := listener.Addr().String()
	slog.Info("local LLM tunnel listening", "addr", tunnelAddr, "target", endpoint.BaseURL)

	// Run the tunnel acceptor in the background.
	go runLocalTunnel(ctx, listener, socketStore, sock.IDDigest)

	// Rewrite the base URL to use the tunnel.
	u.Host = tunnelAddr
	endpoint.BaseURL = u.String()

	return nil
}

// runLocalTunnel accepts connections on the listener and forwards them
// through the SSH session to the target host.
func runLocalTunnel(ctx context.Context, listener net.Listener, store *SocketStore, sockDigest digest.Digest) {
	var wg sync.WaitGroup
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			slog.Warn("local tunnel accept error", "error", err)
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			upstream, err := store.ConnectSocket(ctx, sockDigest)
			if err != nil {
				slog.Warn("local tunnel connect error", "error", err)
				return
			}
			if err := sshforward.Copy(ctx, conn, upstream, upstream.CloseSend); err != nil {
				slog.Warn("local tunnel copy error", "error", err)
			}
		}()
	}
	wg.Wait()
}
