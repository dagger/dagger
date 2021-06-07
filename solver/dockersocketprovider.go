package solver

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	DockerSocketID   = "docker.sock"
	DockerSocketPath = "/var/run/docker.sock"
)

type DockerSocketProvider struct {
}

func NewDockerSocketProvider() session.Attachable {
	return &DockerSocketProvider{}
}

func (sp *DockerSocketProvider) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, sp)
}

func (sp *DockerSocketProvider) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	id := sshforward.DefaultID
	if req.ID != "" {
		id = req.ID
	}
	if id != DockerSocketID {
		return &sshforward.CheckAgentResponse{}, fmt.Errorf("invalid socket forward key %s", id)
	}
	return &sshforward.CheckAgentResponse{}, nil
}

func (sp *DockerSocketProvider) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	id := sshforward.DefaultID

	opts, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object

	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		id = v[0]
	}

	if id != DockerSocketID {
		return fmt.Errorf("invalid socket forward key %s", id)
	}

	conn, err := net.DialTimeout("unix", DockerSocketPath, time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", DockerSocketPath, err)
	}
	defer conn.Close()

	return sshforward.Copy(context.TODO(), conn, stream, nil)
}
