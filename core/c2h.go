package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/dagger/dagger/engine/buildkit"
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
	logWriter          io.Writer
}

func (d *c2hTunnel) Tunnel(ctx context.Context) (rerr error) {
	hostCaller, err := d.bk.SessionManager.Get(ctx, d.sessionID, true)
	if err != nil {
		return fmt.Errorf("failed to get buildkit session caller %s: %w", d.sessionID, err)
	}

	getFrontend := func(port PortForward) int {
		if port.Frontend != nil {
			return *port.Frontend
		}
		return port.Backend
	}

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

			frontend := getFrontend(port)
			listener, err := buildkit.RunInNetNS(ctx, d.bk, d.ns, func() (net.Listener, error) {
				return net.Listen(port.Protocol.Network(), fmt.Sprintf(":%d", frontend))
			})
			if err != nil {
				fmt.Fprintf(d.logWriter, "failed to listen on %s:%d : %v\n", port.Protocol.Network(), frontend, err)
				return fmt.Errorf("failed to listen on network namespace: %w", err)
			}
			fmt.Fprintf(d.logWriter, "listening on %s:%d\n", port.Protocol.Network(), frontend)

			go func() {
				<-ctx.Done()
				listener.Close()
			}()

			for {
				downstreamConn, err := listener.Accept()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						fmt.Fprintf(d.logWriter, "listener closed\n")
						return nil
					}
					return fmt.Errorf("fatal accept error: %w", err)
				}

				fmt.Fprintf(d.logWriter, "handling %s\n", downstreamConn.RemoteAddr())

				upstreamClient, err := sshforward.NewSSHClient(hostCaller.Conn()).ForwardAgent(
					metadata.NewOutgoingContext(ctx, map[string][]string{
						sshforward.KeySSHID: {upstreamSock.SSHID()},
					}),
				)
				if err != nil {
					fmt.Fprintf(d.logWriter, "failed to create upstream client %s: %v\n", upstreamSock.SSHID(), err)
					return fmt.Errorf("failed to create upstream client %s: %w", upstreamSock.SSHID(), err)
				}

				proxyConnPool.Go(func(ctx context.Context) error {
					err := sshforward.Copy(ctx, downstreamConn, upstreamClient, upstreamClient.CloseSend)
					if err != nil {
						fmt.Fprintf(d.logWriter, "failed to copy: %v\n", err)
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
		fmt.Fprintf(d.logWriter, "tunnel failed: %v\n", rerr)
	}
	return rerr
}
