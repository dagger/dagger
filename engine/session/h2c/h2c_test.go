package h2c

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type pipeListener struct {
	accept chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.accept:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (*pipeListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234} }

type listenerStream struct {
	grpc.ServerStream
	ctx      context.Context
	requests chan *ListenRequest
	sent     chan *ListenResponse
}

func (s *listenerStream) Context() context.Context { return s.ctx }

func (s *listenerStream) Send(response *ListenResponse) error {
	select {
	case s.sent <- response:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *listenerStream) Recv() (*ListenRequest, error) {
	select {
	case req := <-s.requests:
		return req, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

type closedWriter struct{ net.Conn }

func (closedWriter) Write([]byte) (int, error) { return 0, net.ErrClosed }

func TestTunnelConnectionsWithSameRemoteAddress(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	listener := &pipeListener{accept: make(chan net.Conn), closed: make(chan struct{})}
	stream := &listenerStream{
		ctx: ctx, requests: make(chan *ListenRequest, 4), sent: make(chan *ListenResponse, 4),
	}
	done := make(chan error, 1)
	go func() { done <- NewTunnelListenerAttachable(ctx).serveListener(stream, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("tunnel listener did not stop")
		}
	})
	receive := func() *ListenResponse {
		t.Helper()
		select {
		case response := <-stream.sent:
			return response
		case <-time.After(5 * time.Second):
			t.Fatal("tunnel listener did not respond")
			return nil
		}
	}
	require.NotEmpty(t, receive().Addr)

	// net.Pipe reports the same remote address for both sockets. Simulate a
	// write racing closure on the first, while its reader is still blocked.
	first, firstPeer := net.Pipe()
	t.Cleanup(func() { firstPeer.Close() })
	listener.accept <- closedWriter{first}
	firstID := receive().ConnId
	second, secondPeer := net.Pipe()
	t.Cleanup(func() { secondPeer.Close() })
	listener.accept <- second
	secondID := receive().ConnId
	require.NotEqual(t, firstID, secondID, "each accepted socket needs its own ID")

	stream.requests <- &ListenRequest{ConnId: firstID, Data: []byte("closed")}
	closed := receive()
	require.Equal(t, firstID, closed.ConnId)
	require.True(t, closed.Close)
	// A late close for the first socket must not close the second, and the
	// write failure must not end the listener's receive loop.
	stream.requests <- &ListenRequest{ConnId: firstID, Close: true}
	stream.requests <- &ListenRequest{ConnId: secondID, Data: []byte("alive")}
	require.NoError(t, secondPeer.SetReadDeadline(time.Now().Add(5*time.Second)))
	data := make([]byte, len("alive"))
	_, err := io.ReadFull(secondPeer, data)
	require.NoError(t, err)
	require.Equal(t, "alive", string(data))

	// Listener shutdown must also close all remaining accepted sockets.
	cancel()
	_, err = secondPeer.Read(data)
	require.ErrorIs(t, err, io.EOF)
}
