package engine

import (
	"context"

	"github.com/moby/buildkit/session/sshforward"
	"github.com/moby/buildkit/session/sshforward/sshprovider"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	sshAuthSockEnv = "SSH_AUTH_SOCK"
)

// Map of id -> handler for that id.
type MergedSocketProviders map[string]sshforward.SSHServer

func (m MergedSocketProviders) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, m)
}

func (m MergedSocketProviders) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	id := sshforward.DefaultID
	if req.ID != "" {
		id = req.ID
	}
	h, ok := m[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no ssh handler for id %s", id)
	}
	return h.CheckAgent(ctx, req)
}

func (m MergedSocketProviders) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	id := sshforward.DefaultID
	opts, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object
	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		id = v[0]
	}
	h, ok := m[id]
	if !ok {
		return status.Errorf(codes.NotFound, "no ssh handler for id %s", id)
	}
	return h.ForwardAgent(stream)
}

func sshAuthSockHandler() (sshforward.SSHServer, error) {
	agentProvider, err := sshprovider.NewSSHAgentProvider([]sshprovider.AgentConfig{{ID: sshAuthSockEnv}})
	if err != nil {
		return nil, err
	}
	handler, ok := agentProvider.(sshforward.SSHServer)
	if !ok {
		return nil, status.Errorf(codes.Internal, "invalid agent provider type: %T", agentProvider)
	}
	return handler, nil
}
