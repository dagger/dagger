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
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}
	switch u.Scheme {
	case "unix", "tcp", "udp":
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported scheme: %q", u.Scheme)
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
	var id string
	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		id = v[0]
	}
	if id == "" {
		return status.Errorf(codes.InvalidArgument, "id is not set")
	}
	u, err := url.Parse(id)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}
	var conn net.Conn
	switch u.Scheme {
	case "unix":
		conn, err = net.Dial("unix", u.Path)
	case "tcp", "udp":
		conn, err = net.Dial(u.Scheme, u.Host)
	default:
		return status.Errorf(codes.InvalidArgument, "unsupported scheme: %q", u.Scheme)
	}
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "dial socket: %v", err)
	}
	return sshforward.Copy(context.TODO(), conn, stream, nil)
}
