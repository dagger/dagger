package core

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"testing"

	engineclient "github.com/dagger/dagger/engine/client"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestSocketAgentDisconnectIsNotTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Read the complete identities request, then close without a reply.
		_, _ = io.CopyN(io.Discard, conn, 5)
	}()
	conn := newTestAttachableConn(t, func(server *grpc.Server) {
		engineclient.SocketProvider{EnableHostNetworkAccess: true}.Register(server)
	})
	ctx, _ := newSessionResourceFallbackTestContext(t, map[string]*grpc.ClientConn{"source": conn})
	ctx, cancel := context.WithTimeout(ctx, 3*sshAgentIdentityTimeout)
	defer cancel()
	_, err = (&Socket{URLVal: "unix://" + path, SourceClientID: "source"}).AgentFingerprints(ctx)
	require.ErrorContains(t, err, "EOF")
	require.NoError(t, ctx.Err(), "a closed agent must reach the caller before its deadline")
}
