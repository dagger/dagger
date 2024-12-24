package client

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/dagger/dagger/engine"
	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type SocketProvider struct {
	EnableHostNetworkAccess bool
}

func (p SocketProvider) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, p)
}

func (p SocketProvider) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	if !p.EnableHostNetworkAccess {
		return nil, status.Errorf(codes.PermissionDenied, "host access is disabled")
	}
	if req.ID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "id is not set")
	}
	u, err := url.Parse(req.ID)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %s", err)
	}
	switch u.Scheme {
	case "unix":
		path := u.Path
		stat, err := os.Stat(path)
		if err != nil {
			return nil, status.Errorf(codes.NotFound, "socket %s not found: %s", u.Path, err)
		}
		if stat.Mode()&os.ModeSocket == 0 {
			return nil, status.Errorf(codes.InvalidArgument, "not a socket: %s", u.Path)
		}
	case "tcp", "udp":
	default:
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: unsupported scheme %q", u.Scheme)
	}
	return &sshforward.CheckAgentResponse{}, nil
}

func (p SocketProvider) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	if !p.EnableHostNetworkAccess {
		return status.Errorf(codes.PermissionDenied, "host access is disabled")
	}
	opts, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return status.Errorf(codes.InvalidArgument, "no metadata")
	}
	v := opts.Get(engine.SocketURLEncodedKey)
	if len(v) == 0 || v[0] == "" {
		return status.Errorf(codes.InvalidArgument, "missing socket url")
	}
	connURL, err := url.Parse(v[0])
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid socket url %q: %s", v[0], err)
	}

	var network, addr string
	dialer := net.Dial
	switch connURL.Scheme {
	case "unix":
		network = "unix"
		addr = connURL.Path
	case "tcp", "udp":
		network = connURL.Scheme
		addr = connURL.Host
	default:
		return status.Errorf(codes.InvalidArgument, "invalid socket url: unsupported scheme %q", connURL.Scheme)
	}

	return (&socketProxy{
		network: network,
		addr:    addr,
		dialer:  dialer,
	}).ForwardAgent(stream)
}

type socketProxy struct {
	network, addr string
	dialer        func(network, addr string) (net.Conn, error)
}

var _ sshforward.SSHServer = &socketProxy{}

func (p *socketProxy) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}

func (p *socketProxy) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	conn, err := p.dialer(p.network, p.addr)
	if err != nil {
		return fmt.Errorf("dialer failed: %w", err)
	}

	err = sshforward.Copy(context.TODO(), conn, stream, nil)
	if err != nil {
		return fmt.Errorf("copy failed: %w", err)
	}
	return nil
}
