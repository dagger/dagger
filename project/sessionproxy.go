package project

import (
	"context"
	"net"

	"github.com/dagger/dagger/router"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var _ session.Attachable = &SessionProxy{}

type SessionProxy struct {
	router *router.Router
}

func NewSessionProxy(router *router.Router) *SessionProxy {
	return &SessionProxy{
		router: router,
	}
}

func (p *SessionProxy) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, p)
}

func (p *SessionProxy) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}

func (p *SessionProxy) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	opts, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return status.Errorf(codes.Internal, "no metadata in context")
	}
	v, ok := opts[sshforward.KeySSHID]
	if !ok || len(v) == 0 || v[0] == "" {
		return status.Errorf(codes.Internal, "no sshid in metadata")
	}
	id := v[0]

	if id != SessionProxySockName {
		return status.Errorf(codes.Internal, "no session proxy connection for id %s", id)
	}
	serverConn, clientConn := net.Pipe()
	go func() {
		_ = p.router.ServeConn(serverConn)
	}()
	return sshforward.Copy(context.TODO(), clientConn, stream, nil)
}
