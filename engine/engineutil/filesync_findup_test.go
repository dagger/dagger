package engineutil

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/session/filesync"
)

// findUpServer is a client's file service. With no answer set it is an older
// client, or a proxy in front of one, that doesn't implement FindUp.
type findUpServer struct {
	filesync.UnimplementedFileSyncServer
	answer *filesync.FindUpResponse
	calls  atomic.Int32
}

func (s *findUpServer) FindUp(ctx context.Context, req *filesync.FindUpRequest) (*filesync.FindUpResponse, error) {
	s.calls.Add(1)
	if s.answer == nil {
		return s.UnimplementedFileSyncServer.FindUp(ctx, req)
	}
	return s.answer, nil
}

type findUpCaller struct {
	conn     *grpc.ClientConn
	supports bool
}

func (c findUpCaller) Conn() *grpc.ClientConn { return c.conn }
func (c findUpCaller) Supports(m string) bool { return c.supports && m == findUpMethod }

func findUpClient(t *testing.T, srv *findUpServer, supports bool) (*Client, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	t.Cleanup(server.Stop)
	filesync.RegisterFileSyncServer(server, srv)
	go func() { _ = server.Serve(listener) }()
	cc, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { cc.Close() })
	client := &Client{
		Opts: &Opts{
			GetClientCaller: func(context.Context, string) (SessionCaller, error) {
				return findUpCaller{conn: cc, supports: supports}, nil
			},
		},
		closeCtx: ctx,
	}
	return client, engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "test"})
}

// The engine asks a client that offers FindUp once, and takes its answer.
func TestFindUpCallerHostPathsAsksOnce(t *testing.T) {
	srv := &findUpServer{answer: &filesync.FindUpResponse{
		Start: "/home/user/a/b",
		Found: map[string]string{".env": "/home/user"},
	}}
	client, ctx := findUpClient(t, srv, true)
	start, found, err := client.FindUpCallerHostPaths(ctx, ".", []string{".env", ".git"})
	require.NoError(t, err)
	require.Equal(t, "/home/user/a/b", start)
	require.Equal(t, map[string]string{".env": "/home/user"}, found)
	require.EqualValues(t, 1, srv.calls.Load())
}

// A client too old to offer FindUp is told apart without a call, so the caller
// can search its directories one stat at a time as before.
func TestFindUpCallerHostPathsOlderClient(t *testing.T) {
	srv := &findUpServer{}
	client, ctx := findUpClient(t, srv, false)
	_, _, err := client.FindUpCallerHostPaths(ctx, ".", []string{".env"})
	require.ErrorIs(t, err, ErrFindUpUnsupported)
	require.Zero(t, srv.calls.Load())
}

// A proxy, such as a scale-out engine's, offers FindUp for every client behind
// it; when the client behind it is too old, its Unimplemented answer means the
// same as a client that doesn't offer the method.
func TestFindUpCallerHostPathsProxyForOlderClient(t *testing.T) {
	srv := &findUpServer{}
	client, ctx := findUpClient(t, srv, true)
	_, _, err := client.FindUpCallerHostPaths(ctx, ".", []string{".env"})
	require.ErrorIs(t, err, ErrFindUpUnsupported)
	require.EqualValues(t, 1, srv.calls.Load())
}
