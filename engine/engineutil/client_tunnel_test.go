package engineutil

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/h2c"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type replayTunnelListener struct {
	h2c.UnimplementedTunnelListenerServer
	stream chan h2c.TunnelListener_ListenServer
}

func (s *replayTunnelListener) Listen(stream h2c.TunnelListener_ListenServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if err := stream.Send(&h2c.ListenResponse{Addr: "127.0.0.1:1234"}); err != nil {
		return err
	}
	s.stream <- stream
	<-stream.Context().Done()
	return nil
}

type tunnelTestCaller struct{ conn *grpc.ClientConn }

func (c tunnelTestCaller) Conn() *grpc.ClientConn { return c.conn }
func (tunnelTestCaller) Supports(string) bool     { return true }

func newReplayHostTunnel(t *testing.T, upstream string) (h2c.TunnelListener_ListenServer, func() error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	t.Cleanup(server.Stop)
	peer := &replayTunnelListener{stream: make(chan h2c.TunnelListener_ListenServer, 1)}
	h2c.RegisterTunnelListenerServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	cc, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { cc.Close() })
	client := &Client{
		Opts: &Opts{
			Dialer: &net.Dialer{},
			GetClientCaller: func(context.Context, string) (SessionCaller, error) {
				return tunnelTestCaller{cc}, nil
			},
		},
		closeCtx: ctx,
	}
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "test"})
	_, closeTunnel, err := client.ListenHostToContainer(ctx, "127.0.0.1:0", "tcp", upstream)
	require.NoError(t, err)
	t.Cleanup(func() {
		cancel()
		_ = closeTunnel()
	})
	return <-peer.stream, closeTunnel
}

func TestHostTunnelReusedConnectionID(t *testing.T) {
	requests := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.Path
		w.Header().Set("Connection", "close")
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	stream, _ := newReplayHostTunnel(t, strings.TrimPrefix(upstream.URL, "http://"))
	// Older listeners use RemoteAddr as their ID. A later TCP connection can
	// reuse that address after the first connection has closed.
	const connID = "127.0.0.1:45678"
	sendRequest := func(path string) {
		t.Helper()
		require.NoError(t, stream.Send(&h2c.ListenResponse{ConnId: connID}))
		require.NoError(t, stream.Send(&h2c.ListenResponse{
			ConnId: connID,
			Data:   []byte("GET " + path + " HTTP/1.1\r\nHost: upstream\r\nConnection: close\r\n\r\n"),
		}))
	}
	sendRequest("/first")
	for {
		response, err := stream.Recv()
		require.NoError(t, err)
		if response.Close {
			break
		}
	}
	require.Equal(t, "/first", <-requests)
	// Buffered data from the retired connection must not open another socket.
	require.NoError(t, stream.Send(&h2c.ListenResponse{
		ConnId: connID,
		Data:   []byte("GET /stale HTTP/1.1\r\nHost: upstream\r\n\r\n"),
	}))
	sendRequest("/second")
	select {
	case path := <-requests:
		require.Equal(t, "/second", path)
	case <-time.After(3 * time.Second):
		t.Fatal("a new connection using a retired tunnel ID did not reach the upstream")
	}
}

func TestHostTunnelCloseUnblocksActiveConnections(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, upstream.(*net.TCPListener).SetDeadline(time.Now().Add(5*time.Second)))
	t.Cleanup(func() { upstream.Close() })
	stream, closeTunnel := newReplayHostTunnel(t, upstream.Addr().String())
	require.NoError(t, stream.Send(&h2c.ListenResponse{ConnId: "active"}))
	conn, err := upstream.Accept()
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	// Start sending while the upstream sends no response and consumes only a
	// single byte. Shutdown must release the forwarding goroutines even while
	// traffic is active and the upstream reader is waiting for a response.
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		data := make([]byte, 64*1024)
		for range 1024 {
			if err := stream.Send(&h2c.ListenResponse{ConnId: "active", Data: data}); err != nil {
				return
			}
		}
	}()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = conn.Read(make([]byte, 1))
	require.NoError(t, err)
	closeDone := make(chan error, 1)
	go func() { closeDone <- closeTunnel() }()
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel shutdown did not unblock active connections")
	}
	select {
	case <-sendDone:
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel shutdown left a sender blocked")
	}
}

func TestHostTunnelPeerCloseRetiresConnection(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, upstream.(*net.TCPListener).SetDeadline(time.Now().Add(5*time.Second)))
	t.Cleanup(func() { upstream.Close() })
	stream, _ := newReplayHostTunnel(t, upstream.Addr().String())
	for range 2 {
		require.NoError(t, stream.Send(&h2c.ListenResponse{ConnId: "peer"}))
		conn, err := upstream.Accept()
		require.NoError(t, err)
		t.Cleanup(func() { conn.Close() })
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
		require.NoError(t, stream.Send(&h2c.ListenResponse{ConnId: "peer", Close: true}))
		_, err = conn.Read(make([]byte, 1))
		require.ErrorIs(t, err, io.EOF)
		response, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, "peer", response.ConnId)
		require.True(t, response.Close)
	}
}
