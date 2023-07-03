package session

import (
	"context"

	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type MergedSocketProvider struct {
	providers []sshforward.SSHServer
	// TODO: enforce this in the session stream proxy
	// EnableHostNetworkAccess bool
}

func (m MergedSocketProvider) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, m)
}

func (m MergedSocketProvider) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	id := sshforward.DefaultID
	if req.ID != "" {
		id = req.ID
	}
	for _, h := range m.providers {
		resp, err := h.CheckAgent(ctx, req)
		if status.Code(err) == codes.NotFound {
			continue
		}
		return resp, err
	}
	return nil, status.Errorf(codes.NotFound, "no ssh handler for id %s", id)
}

func (m MergedSocketProvider) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	id := sshforward.DefaultID
	opts, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object
	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		id = v[0]
	}

	for _, h := range m.providers {
		err := h.ForwardAgent(stream)
		if status.Code(err) == codes.NotFound {
			continue
		}
		return err
	}
	return status.Errorf(codes.NotFound, "no ssh handler for id %s", id)
}

// Map of id -> handler for that id.
type NamedSocketProviders map[string]sshforward.SSHServer

func (p NamedSocketProviders) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	id := sshforward.DefaultID
	if req.ID != "" {
		id = req.ID
	}
	h, ok := p[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "no ssh handler for id %s", id)
	}
	return h.CheckAgent(ctx, req)
}

func (p NamedSocketProviders) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	id := sshforward.DefaultID
	opts, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object
	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		id = v[0]
	}

	h, ok := p[id]
	if !ok {
		return status.Errorf(codes.NotFound, "no ssh handler for id %s", id)
	}

	return h.ForwardAgent(stream)
}
