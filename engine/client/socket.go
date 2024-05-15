package client

import (
	"context"
	"net"
	"net/url"

	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type SocketProvider struct {
	EnableHostNetworkAccess bool
	// optional, defaults to net.Dial if not set
	Dialer func(network, addr string) (net.Conn, error)
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
	case "unix", "tcp", "udp":
	default:
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: unsupported scheme %q", u.Scheme)
	}
	return &sshforward.CheckAgentResponse{}, nil
}

func (p SocketProvider) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	if !p.EnableHostNetworkAccess {
		return status.Errorf(codes.PermissionDenied, "host access is disabled")
	}
	opts, ok := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object
	if !ok {
		return status.Errorf(codes.InvalidArgument, "no metadata")
	}
	var connURL *url.URL
	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		var err error
		connURL, err = url.Parse(v[0])
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid id: %s", err)
		}
	}
	var network, addr string
	switch connURL.Scheme {
	case "unix":
		network = "unix"
		addr = connURL.Path
	case "tcp", "udp":
		network = connURL.Scheme
		addr = connURL.Host
	default:
		return status.Errorf(codes.InvalidArgument, "invalid id: unsupported scheme %q", connURL.Scheme)
	}

	dialer := p.Dialer
	if dialer == nil {
		dialer = net.Dial
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
		return err
	}

	return sshforward.Copy(context.TODO(), conn, stream, nil)
}
