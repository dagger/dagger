package client

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// CloseSend lets the peer continue sending. Once the request is cancelled,
// however, forwarding must finish even if that peer keeps its side open.
func TestSocketProxyCancellationAfterCloseSend(t *testing.T) {
	backend, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer backend.Close()
	peerSawEOF := make(chan struct{})
	releasePeer := make(chan struct{})
	defer close(releasePeer)
	go func() {
		conn, err := backend.AcceptTCP()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := io.Copy(io.Discard, conn); err == nil {
			close(peerSawEOF)
		}
		<-releasePeer
	}()
	closed := make(chan struct{})
	proxy := &cancellationSocketProxy{
		socketProxy: &socketProxy{
			network: "tcp",
			addr:    backend.Addr().String(),
			dialer: func(network, _ string) (net.Conn, error) {
				conn, err := net.DialTCP(network, nil, backend.Addr().(*net.TCPAddr))
				if err != nil {
					return nil, err
				}
				return &closeNotifyTCPConn{TCPConn: conn, closed: closed}, nil
			},
		},
		returned: make(chan struct{}),
	}
	transport := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	sshforward.RegisterSSHServer(server, proxy)
	defer server.Stop()
	go func() { _ = server.Serve(transport) }()
	conn, err := grpc.NewClient("passthrough:///cancel-probe",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return transport.DialContext(ctx)
		}))
	require.NoError(t, err)
	defer conn.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := sshforward.NewSSHClient(conn).ForwardAgent(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.SendMsg(&sshforward.BytesMessage{Data: []byte("request")}))
	require.NoError(t, stream.CloseSend())
	select {
	case <-peerSawEOF:
	case <-time.After(5 * time.Second):
		t.Fatal("the peer did not see the client's end of data")
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled forwarding kept the socket open after CloseSend")
	}
	select {
	case <-proxy.returned:
	case <-time.After(5 * time.Second):
		t.Fatal("forwarding handler did not return after cancellation")
	}
}

type closeNotifyTCPConn struct {
	*net.TCPConn
	closed chan struct{}
	once   sync.Once
}

func (c *closeNotifyTCPConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.TCPConn.Close()
}

type cancellationSocketProxy struct {
	*socketProxy
	returned chan struct{}
}

func (p *cancellationSocketProxy) ForwardAgent(s sshforward.SSH_ForwardAgentServer) error {
	defer close(p.returned)
	return p.socketProxy.ForwardAgent(s)
}
