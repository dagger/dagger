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

// localTunnel holds the state of a running c2h tunnel for a local LLM.
type localTunnel struct {
	listener net.Listener
	cancel   context.CancelFunc
}

// Stop shuts down the tunnel listener and waits for connections to drain.
func (t *localTunnel) Stop() {
	t.cancel()
	t.listener.Close()
}

// setupLocalTunnel sets up a c2h (container-to-host) tunnel for a local LLM
// endpoint. The tunnel forwards connections from a local listener in the
// engine process through the client's SSH session to the target host.
//
// It rewrites endpoint.BaseURL to point at the local tunnel listener and
// returns the running tunnel (caller should call Stop when done).
func setupLocalTunnel(ctx context.Context, query *Query, endpoint *LLMEndpoint) (*localTunnel, error) {
	u, err := url.Parse(endpoint.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse local endpoint URL: %w", err)
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
		return nil, fmt.Errorf("parse port %q from %s: %w", port, endpoint.BaseURL, err)
	}

	// Register an IP socket for the target host:port so we can tunnel
	// through the client's SSH session.
	socketStore, err := query.Sockets(ctx)
	if err != nil {
		return nil, fmt.Errorf("get socket store: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get client metadata: %w", err)
	}

	pf := PortForward{
		Backend:  backendPort,
		Protocol: NetworkProtocolTCP,
	}

	accessor, err := GetHostIPSocketAccessor(ctx, query, host, pf)
	if err != nil {
		return nil, fmt.Errorf("get host IP socket accessor: %w", err)
	}

	sock := &Socket{IDDigest: hashutil.HashStrings(accessor)}
	if err := socketStore.AddIPSocket(sock, clientMetadata.ClientID, host, pf); err != nil {
		return nil, fmt.Errorf("register IP socket: %w", err)
	}

	// Create a local TCP listener that forwards connections through the SSH
	// session to the target host.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for local tunnel: %w", err)
	}

	tunnelAddr := listener.Addr().String()
	slog.Info("local LLM tunnel listening", "addr", tunnelAddr, "target", endpoint.BaseURL)

	// Decouple the tunnel's lifetime from the request context. The tunnel
	// must stay alive for as long as the LLM endpoint is in use, not just
	// the duration of the Endpoint() call that created it.
	tunnelCtx, tunnelCancel := context.WithCancel(context.WithoutCancel(ctx))

	// Run the tunnel acceptor in the background.
	go runLocalTunnel(tunnelCtx, listener, socketStore, sock.IDDigest)

	// Rewrite the base URL to use the tunnel.
	u.Host = tunnelAddr
	endpoint.BaseURL = u.String()

	return &localTunnel{listener: listener, cancel: tunnelCancel}, nil
}

// runLocalTunnel accepts connections on the listener and forwards them
// through the SSH session to the target host.
func runLocalTunnel(ctx context.Context, listener net.Listener, store *SocketStore, sockDigest digest.Digest) {
	slog.Info("local LLM tunnel goroutine started", "addr", listener.Addr().String())
	var wg sync.WaitGroup
	go func() {
		<-ctx.Done()
		slog.Info("local LLM tunnel context done, closing listener")
		listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				slog.Info("local LLM tunnel listener closed")
				break
			}
			slog.Warn("local tunnel accept error", "error", err)
			break
		}
		slog.Info("local LLM tunnel accepted connection", "remote", conn.RemoteAddr().String())
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			upstream, err := store.ConnectSocket(ctx, sockDigest)
			if err != nil {
				slog.Warn("local tunnel connect error", "error", err)
				return
			}
			slog.Info("local LLM tunnel connected upstream")
			if err := sshforward.Copy(ctx, conn, upstream, upstream.CloseSend); err != nil {
				slog.Warn("local tunnel copy error", "error", err)
			}
			slog.Info("local LLM tunnel connection closed")
		}()
	}
	slog.Info("local LLM tunnel goroutine exiting")
	wg.Wait()
}
