package client

import (
	"context"

	"github.com/dagger/dagger/core"
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
	socket, err := core.SocketID(req.ID).ToSocket()
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}
	if !socket.IsHost() {
		return nil, status.Errorf(codes.InvalidArgument, "id is not a host socket")
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
	socket, err := core.SocketID(id).ToSocket()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}
	if !socket.IsHost() {
		return status.Errorf(codes.InvalidArgument, "id is not a host socket")
	}
	socketServer, err := socket.Server()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid socket: %v", err)
	}
	return socketServer.ForwardAgent(stream)
}
