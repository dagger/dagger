package solver

import (
	"context"
	"fmt"

	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/sshforward"
	"go.dagger.io/dagger/plancontext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type SocketProvider struct {
	pctx *plancontext.Context
}

func NewDockerSocketProvider(pctx *plancontext.Context) session.Attachable {
	return &SocketProvider{pctx}
}

func (sp *SocketProvider) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, sp)
}

func (sp *SocketProvider) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}

func (sp *SocketProvider) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	id := sshforward.DefaultID

	opts, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object

	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		id = v[0]
	}

	service := sp.pctx.Sockets.Get(id)
	if service == nil {
		return fmt.Errorf("invalid socket id %q", id)
	}

	conn, err := dialService(service)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", id, err)
	}
	defer conn.Close()

	return sshforward.Copy(context.TODO(), conn, stream, nil)
}
