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
	"google.golang.org/grpc/metadata"
)

type c2hTunnel struct {
	bk                 *buildkit.Client
	ns                 buildkit.Namespaced
	upstreamHost       string
	tunnelServiceHost  string
	tunnelServicePorts []PortForward
	sessionID          string
}

func (d *c2hTunnel) Tunnel(ctx context.Context) (rerr error) {
	hostCaller, err := d.bk.SessionManager.Get(ctx, d.sessionID, true)
	if err != nil {
		return fmt.Errorf("failed to get buildkit session caller %s: %w", d.sessionID, err)
	}

	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listenerPool := pool.New().WithContext(ctx)
	proxyConnPool := pool.New().WithContext(ctx)
	for _, port := range d.tunnelServicePorts {
		port := port
		listenerPool.Go(func(ctx context.Context) error {
			defer cancel() // if one exits, all should exit
			upstreamSock := NewHostIPSocket(
				port.Protocol.Network(),
				fmt.Sprintf("%s:%d", d.upstreamHost, port.Backend),
				d.sessionID,
			)

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

				upstreamClient, err := sshforward.NewSSHClient(hostCaller.Conn()).ForwardAgent(
					metadata.NewOutgoingContext(ctx, map[string][]string{
						sshforward.KeySSHID: {upstreamSock.SSHID()},
					}),
				)
				if err != nil {
					connSlog.Error("failed to create upstream client", "id", upstreamSock.SSHID(), "error", err)
					return fmt.Errorf("failed to create upstream client %s: %w", upstreamSock.SSHID(), err)
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
