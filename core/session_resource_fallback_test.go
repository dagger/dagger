package core

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/buildkit/session/secrets"
	"github.com/dagger/dagger/internal/buildkit/session/sshforward"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

type testSecretsServer struct {
	secrets.UnimplementedSecretsServer
	data []byte
}

func (srv *testSecretsServer) GetSecret(context.Context, *secrets.GetSecretRequest) (*secrets.GetSecretResponse, error) {
	return &secrets.GetSecretResponse{Data: append([]byte(nil), srv.data...)}, nil
}

type testSSHServer struct {
	sshforward.UnimplementedSSHServer
	calls atomic.Int32
}

func (srv *testSSHServer) CheckAgent(context.Context, *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}

func (srv *testSSHServer) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	srv.calls.Add(1)
	msg, err := stream.Recv()
	if err != nil {
		return err
	}
	return stream.Send(&sshforward.BytesMessage{Data: append([]byte("ok:"), msg.Data...)})
}

func newTestAttachableConn(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	register(server)
	go func() {
		_ = server.Serve(listener)
	}()

	conn, err := grpc.NewClient("passthrough:bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	assert.NilError(t, err)
	conn.Connect()

	t.Cleanup(func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	})
	return conn
}

func newSessionResourceFallbackTestContext(t *testing.T, attachables map[string]*grpc.ClientConn) (context.Context, *dagql.Cache) {
	t.Helper()

	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	query := &Query{
		Server: &mockServer{
			attachables: attachables,
		},
	}
	ctx = ContextWithQuery(ctx, query)
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		SessionID: "test-session",
		ClientID:  "dead-client",
	})
	return ctx, cache
}

func TestSecretPlaintextFallsBackToAvailableSessionResourceBinding(t *testing.T) {
	conn := newTestAttachableConn(t, func(server *grpc.Server) {
		secrets.RegisterSecretsServer(server, &testSecretsServer{data: []byte("live-secret")})
	})
	ctx, cache := newSessionResourceFallbackTestContext(t, map[string]*grpc.ClientConn{
		"live-client": conn,
	})

	handle := dagql.SessionResourceHandle("test-secret-handle")
	assert.NilError(t, cache.BindSessionResource(ctx, "test-session", "dead-client", handle, &Secret{
		URIVal:         "env://TOKEN",
		SourceClientID: "dead-client",
	}))
	assert.NilError(t, cache.BindSessionResource(ctx, "test-session", "live-client", handle, &Secret{
		URIVal:         "env://TOKEN",
		SourceClientID: "live-client",
	}))

	plaintext, err := (&Secret{Handle: handle}).Plaintext(ctx)
	assert.NilError(t, err)
	assert.DeepEqual(t, plaintext, []byte("live-secret"))
}

func TestSocketForwardAgentClientFallsBackToAvailableSessionResourceBinding(t *testing.T) {
	sshSrv := &testSSHServer{}
	conn := newTestAttachableConn(t, func(server *grpc.Server) {
		sshforward.RegisterSSHServer(server, sshSrv)
	})
	ctx, cache := newSessionResourceFallbackTestContext(t, map[string]*grpc.ClientConn{
		"live-client": conn,
	})

	handle := dagql.SessionResourceHandle("test-socket-handle")
	assert.NilError(t, cache.BindSessionResource(ctx, "test-session", "dead-client", handle, &Socket{
		Kind:           SocketKindUnixOpaque,
		URLVal:         "unix:///tmp/dead.sock",
		SourceClientID: "dead-client",
	}))
	assert.NilError(t, cache.BindSessionResource(ctx, "test-session", "live-client", handle, &Socket{
		Kind:           SocketKindUnixOpaque,
		URLVal:         "unix:///tmp/live.sock",
		SourceClientID: "live-client",
	}))

	stream, err := (&Socket{Handle: handle}).ForwardAgentClient(ctx)
	assert.NilError(t, err)
	err = stream.Send(&sshforward.BytesMessage{Data: []byte("ping")})
	assert.NilError(t, err)
	msg, err := stream.Recv()
	assert.NilError(t, err)
	assert.DeepEqual(t, msg.Data, []byte("ok:ping"))
	assert.NilError(t, stream.CloseSend())
	assert.Assert(t, is.Equal(int32(1), sshSrv.calls.Load()))
}
