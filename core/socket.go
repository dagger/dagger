package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sync"

	"github.com/dagger/dagger/engine"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/sshforward"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type Socket struct {
	Name string `json:"name,omitempty"`

	// If set, the id of the buildkit session the socket will be connected through.
	BuildkitSessionID string `json:"buildkit_session_id,omitempty"`

	// Unix
	HostPath string `json:"host_path,omitempty"`

	// IP
	HostProtocol string `json:"host_protocol,omitempty"`
	HostAddr     string `json:"host_addr,omitempty"`
}

func (*Socket) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Socket",
		NonNull:   true,
	}
}

func (*Socket) TypeDescription() string {
	return "A Unix or TCP/IP socket that can be mounted into a container."
}

func NewHostUnixSocket(name string, absPath string, buildkitSessionID string) *Socket {
	return &Socket{
		Name:              name,
		HostPath:          absPath,
		BuildkitSessionID: buildkitSessionID,
	}
}

func NewHostIPSocket(proto string, addr string, buildkitSessionID string) *Socket {
	return &Socket{
		HostAddr:          addr,
		HostProtocol:      proto,
		BuildkitSessionID: buildkitSessionID,
	}
}

func (socket *Socket) LLBID() string {
	return socket.Name
}

func (socket *Socket) URLEncoded() string {
	u := &url.URL{
		Fragment: socket.BuildkitSessionID,
	}
	switch {
	case socket.HostPath != "":
		u.Scheme = "unix"
		u.Path = socket.HostPath
	default:
		u.Scheme = socket.HostProtocol
		u.Host = socket.HostAddr
	}
	return u.String()
}

func (socket *Socket) Network() string {
	switch {
	case socket.HostPath != "":
		return "unix"
	default:
		return socket.HostProtocol
	}
}

func (socket *Socket) Addr() string {
	switch {
	case socket.HostPath != "":
		return socket.HostPath
	default:
		return socket.HostAddr
	}
}

func NewSocketStore(bkSessionManager *bksession.Manager, spanCtx trace.SpanContext) *SocketStore {
	return &SocketStore{
		bkSessionManager: bkSessionManager,
		spanCtx:          spanCtx,
		sockets:          map[string]*Socket{},
	}
}

type SocketStore struct {
	bkSessionManager *bksession.Manager
	spanCtx          trace.SpanContext

	sockets map[string]*Socket
	mu      sync.RWMutex
}

var _ sshforward.SSHServer = &SocketStore{}

func (p *SocketStore) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	p.mu.RLock()
	sock, ok := p.sockets[req.GetID()]
	p.mu.RUnlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound, "socket %s not found", req.GetID())
	}

	buildkitSessionID := sock.BuildkitSessionID
	if buildkitSessionID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "missing buildkit session id")
	}
	caller, err := p.bkSessionManager.Get(ctx, buildkitSessionID, true)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get buildkit session: %s", err)
	}

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(engine.SocketURLEncodedKey, sock.URLEncoded()))
	ctx = trace.ContextWithSpanContext(ctx, p.spanCtx) // ensure server's span context is propagated

	return sshforward.NewSSHClient(caller.Conn()).CheckAgent(ctx, &sshforward.CheckAgentRequest{
		ID: sock.URLEncoded(),
	})
}

func (p *SocketStore) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	opts, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Errorf(codes.InvalidArgument, "no metadata")
	}

	v, ok := opts[sshforward.KeySSHID]
	if !ok || len(v) == 0 || v[0] == "" {
		return status.Errorf(codes.InvalidArgument, "missing ssh id")
	}

	p.mu.RLock()
	sock, ok := p.sockets[v[0]]
	p.mu.RUnlock()
	if !ok {
		return status.Errorf(codes.NotFound, "socket %s not found", v[0])
	}

	opts.Set(engine.SocketURLEncodedKey, sock.URLEncoded())
	ctx = metadata.NewOutgoingContext(ctx, opts)
	ctx = trace.ContextWithSpanContext(ctx, p.spanCtx) // ensure server's span context is propagated

	buildkitSessionID := sock.BuildkitSessionID
	if buildkitSessionID == "" {
		return status.Errorf(codes.InvalidArgument, "missing buildkit session id")
	}
	caller, err := p.bkSessionManager.Get(ctx, buildkitSessionID, true)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get buildkit session: %s", err)
	}

	forwardAgentClient, err := sshforward.NewSSHClient(caller.Conn()).ForwardAgent(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get forward agent client: %s", err)
	}

	return proxyStream[sshforward.BytesMessage](ctx, forwardAgentClient, stream)
}

func (p *SocketStore) AddSocket(name string, sock *Socket) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sockets[name] = sock
	return nil
}

func (p *SocketStore) GetSocket(name string) (*Socket, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	sock, ok := p.sockets[name]
	if !ok {
		return nil, fmt.Errorf("socket %q not found", name)
	}
	return sock, nil
}

func (p *SocketStore) Register(srv *grpc.Server) {
	sshforward.RegisterSSHServer(srv, p)
}

func proxyStream[T any](ctx context.Context, clientStream grpc.ClientStream, serverStream grpc.ServerStream) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var eg errgroup.Group
	var done bool
	eg.Go(func() (rerr error) {
		defer func() {
			clientStream.CloseSend()
			if rerr == io.EOF {
				rerr = nil
			}
			if errors.Is(rerr, context.Canceled) && done {
				rerr = nil
			}
			if rerr != nil {
				cancel()
			}
		}()
		for {
			msg, err := withContext(ctx, func() (*T, error) {
				var msg T
				err := serverStream.RecvMsg(&msg)
				return &msg, err
			})
			if err != nil {
				return err
			}
			if err := clientStream.SendMsg(msg); err != nil {
				return err
			}
		}
	})
	eg.Go(func() (rerr error) {
		defer func() {
			if rerr == io.EOF {
				rerr = nil
			}
			if rerr == nil {
				done = true
			}
			cancel()
		}()
		for {
			msg, err := withContext(ctx, func() (*T, error) {
				var msg T
				err := clientStream.RecvMsg(&msg)
				return &msg, err
			})
			if err != nil {
				return err
			}
			if err := serverStream.SendMsg(msg); err != nil {
				return err
			}
		}
	})
	return eg.Wait()
}

// withContext adapts a blocking function to a context-aware function. It's
// up to the caller to ensure that the blocking function f will unblock at
// some time, otherwise there can be a goroutine leak.
func withContext[T any](ctx context.Context, f func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := f()
		ch <- result{v, err}
	}()
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case r := <-ch:
		return r.v, r.err
	}
}
