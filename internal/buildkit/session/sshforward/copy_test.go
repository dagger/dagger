package sshforward

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A server-side socket can stop sending before it stops receiving. Propagate
// that EOF to the client without losing the client's remaining writes.
func TestCopyHalfClose(t *testing.T) {
	for _, first := range []string{"client", "server"} {
		t.Run(first, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			frontend, frontendProxy := copyTestTCPPair(t)
			backend, backendProxy := copyTestTCPPair(t)
			toServer := make(chan *BytesMessage, 8)
			toClient := make(chan *BytesMessage, 8)
			clientStream := copyTestStream{ctx: ctx, in: toClient, out: toServer}
			serverStream := copyTestStream{ctx: ctx, in: toServer, out: toClient}
			done := make(chan error, 2)
			go func() {
				done <- Copy(ctx, frontendProxy, clientStream, func() error {
					close(toServer)
					return nil
				})
			}()
			go func() { done <- Copy(ctx, backendProxy, serverStream, nil) }()

			firstWriter, firstReader := frontend, backend
			if first == "server" {
				firstWriter, firstReader = backend, frontend
			}
			_, err := firstWriter.Write([]byte("first direction"))
			require.NoError(t, err)
			require.NoError(t, firstWriter.CloseWrite())
			require.NoError(t, firstReader.SetReadDeadline(time.Now().Add(3*time.Second)))
			firstData, err := io.ReadAll(firstReader)
			require.NoError(t, err, "EOF must arrive before the other direction closes")
			require.Equal(t, "first direction", string(firstData))
			_, err = firstReader.Write([]byte("reverse direction after EOF"))
			require.NoError(t, err)
			require.NoError(t, firstReader.CloseWrite())
			require.NoError(t, firstWriter.SetReadDeadline(time.Now().Add(3*time.Second)))
			reverseData, err := io.ReadAll(firstWriter)
			require.NoError(t, err)
			require.Equal(t, "reverse direction after EOF", string(reverseData))

			for range 2 {
				select {
				case err := <-done:
					require.NoError(t, err)
				case <-ctx.Done():
					t.Fatal("forwarding did not finish after both peers closed their writes")
				}
			}
		})
	}
}

func copyTestTCPPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer listener.Close()
	client, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	server, err := listener.AcceptTCP()
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })
	return client, server
}

type copyTestStream struct {
	ctx context.Context
	in  <-chan *BytesMessage
	out chan<- *BytesMessage
}

func (s copyTestStream) SendMsg(message interface{}) error {
	copy := *message.(*BytesMessage)
	copy.Data = append([]byte(nil), copy.Data...)
	select {
	case s.out <- &copy:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s copyTestStream) RecvMsg(message interface{}) error {
	select {
	case received, ok := <-s.in:
		if !ok {
			return io.EOF
		}
		*message.(*BytesMessage) = *received
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}
