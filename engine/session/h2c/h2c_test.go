package h2c

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// fakeListenServer is a minimal in-memory TunnelListener_ListenServer. Only
// Send/Recv are exercised by Listen; the embedded grpc.ServerStream satisfies
// the rest of the interface but is never called.
type fakeListenServer struct {
	grpc.ServerStream
	recv chan *ListenRequest
	sent chan *ListenResponse
}

func (f *fakeListenServer) Send(resp *ListenResponse) error {
	f.sent <- resp
	return nil
}

func (f *fakeListenServer) Recv() (*ListenRequest, error) {
	req, ok := <-f.recv
	if !ok {
		return nil, io.EOF
	}
	return req, nil
}

// TestListenReleasesConnOnClientClose is a regression test for a connection
// leak: when the local client closes its end of an accepted conn, the per-conn
// goroutine used to return without closing the conn, deleting it from the map,
// or telling the peer. That leaked an FD per connection until the accept loop
// eventually failed and the tunnel stopped accepting connections.
func TestListenReleasesConnOnClientClose(t *testing.T) {
	fake := &fakeListenServer{
		recv: make(chan *ListenRequest, 8),
		sent: make(chan *ListenResponse, 8),
	}

	// Initial request asking the host to listen.
	fake.recv <- &ListenRequest{Protocol: "tcp", Addr: "127.0.0.1:0"}

	attach := NewTunnelListenerAttachable(context.Background())
	listenDone := make(chan error, 1)
	go func() {
		listenDone <- attach.Listen(fake)
	}()

	// First response carries the bound address.
	addr := recvResp(t, fake.sent).GetAddr()
	require.NotEmpty(t, addr)

	// Connect as a local client, then immediately close (EOF on the read side).
	client, err := net.Dial("tcp", addr)
	require.NoError(t, err)

	// The accept triggers a connID notification.
	connID := recvResp(t, fake.sent).GetConnId()
	require.NotEmpty(t, connID)

	require.NoError(t, client.Close())

	// The fix makes the host propagate a Close for that connID; on the old
	// code nothing was sent and this times out.
	gotClose := false
	for !gotClose {
		resp := recvResp(t, fake.sent)
		if resp.GetConnId() == connID && resp.GetClose() {
			gotClose = true
		}
	}
	require.True(t, gotClose)

	// Shut the listener down cleanly.
	close(fake.recv)
	select {
	case err := <-listenDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Listen did not return")
	}
}

func recvResp(t *testing.T, ch <-chan *ListenResponse) *ListenResponse {
	t.Helper()
	select {
	case resp := <-ch:
		return resp
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ListenResponse")
		return nil
	}
}
