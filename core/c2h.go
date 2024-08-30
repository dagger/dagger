package core

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/slog"
	"github.com/moby/buildkit/session/sshforward"
	"github.com/sourcegraph/conc/pool"
)

type c2hTunnel struct {
	bk        *buildkit.Client
	ns        buildkit.Namespaced
	socks     []*Socket
	sockStore *SocketStore
}

func (d *c2hTunnel) Tunnel(ctx context.Context) (rerr error) {
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(errors.New("tunnel finished"))
	listenerPool := pool.New().WithContext(ctx)
	proxyConnPool := pool.New().WithContext(ctx)
	for _, sock := range d.socks {
		listenerPool.Go(func(ctx context.Context) error {
			defer cancel(errors.New("tunnel listener done")) // if one exits, all should exit

			port, ok := d.sockStore.GetSocketPortForward(sock.IDDigest)
			if !ok {
				return fmt.Errorf("socket not found: %s", sock.IDDigest)
			}
			frontend := port.FrontendOrBackendPort()

			srvSlog := slog.With(
				"protocol", port.Protocol.Network(),
				"frontend", frontend,
				"backend", port.Backend,
			)

			listener, err := buildkit.RunInNetNS(ctx, d.bk, d.ns, func() (net.Listener, error) {
				return net.Listen(port.Protocol.Network(), fmt.Sprintf(":%d", frontend))
			})
			if err != nil {
				srvSlog.Error("failed to listen", "error", err)
				return fmt.Errorf("failed to listen on network namespace: %w", err)
			}

			srvSlog.Info("listening", "addr", listener.Addr())

			go func() {
				<-ctx.Done()
				listener.Close()
			}()

			for {
				downstreamConn, err := listener.Accept()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						srvSlog.Debug("listener closed")
						return nil
					}
					return fmt.Errorf("fatal accept error: %w", err)
				}

				connSlog := slog.With("addr", downstreamConn.RemoteAddr())

				connSlog.Debug("handling connection")

				urlEncoded, ok := d.sockStore.GetSocketURLEncoded(sock.IDDigest)
				if !ok {
					connSlog.Error("socket not found", "id", sock.IDDigest)
					return fmt.Errorf("socket not found: %s", sock.IDDigest)
				}
				upstreamClient, err := d.sockStore.ConnectSocket(ctx, sock.IDDigest)
				if err != nil {
					connSlog.Error("failed to create upstream client", "id", urlEncoded, "error", err)
					return fmt.Errorf("failed to create upstream client %s: %w", urlEncoded, err)
				}

				proxyConnPool.Go(func(ctx context.Context) error {
					err := sshforward.Copy(ctx, downstreamConn, upstreamClient, upstreamClient.CloseSend)
					if err != nil {
						connSlog.Error("failed to copy data", "error", err)
					}
					return err
				})
			}
		})
	}

	if err := listenerPool.Wait(); err != nil {
		rerr = errors.Join(rerr, fmt.Errorf("listener pool failed: %w", err))
	}
	if err := proxyConnPool.Wait(); err != nil {
		rerr = errors.Join(rerr, fmt.Errorf("proxy conn pool failed: %w", err))
	}
	if rerr != nil {
		slog.Error("tunnel finished with errors", "error", rerr)
	}
	return rerr
}
