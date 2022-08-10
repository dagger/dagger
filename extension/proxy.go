package extension

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/dagger/cloak/router"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var _ session.Attachable = &APIProxy{}

type APIProxy struct {
	router *router.Router
}

func NewAPIProxy(router *router.Router) *APIProxy {
	return &APIProxy{
		router: router,
	}
}

func (p *APIProxy) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, p)
}

func (p *APIProxy) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}

func (p *APIProxy) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	opts, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return status.Errorf(codes.Internal, "no metadata in context")
	}
	v, ok := opts[sshforward.KeySSHID]
	if !ok || len(v) == 0 || v[0] == "" {
		return status.Errorf(codes.Internal, "no sshid in metadata")
	}
	id := v[0]
	if id != daggerSockName {
		return status.Errorf(codes.Internal, "no api connection for id %s", id)
	}

	serverConn, clientConn := net.Pipe()
	go func() {
		_ = p.router.ServeConn(serverConn)
	}()
	return sshforward.Copy(context.TODO(), clientConn, stream, nil)
}

// FIXME
// converts a pre-existing net.Conn into a net.Listener that returns the conn
type singleConnListener struct {
	conn net.Conn
	l    sync.Mutex
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.l.Lock()
	defer l.l.Unlock()

	if l.conn == nil {
		return nil, io.ErrClosedPipe
	}
	c := l.conn
	l.conn = nil
	return c, nil
}

func (l *singleConnListener) Addr() net.Addr {
	return nil
}

func (l *singleConnListener) Close() error {
	return nil
}
