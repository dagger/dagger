package core

import (
	"context"

	"github.com/moby/buildkit/client/connhelper"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
)

var _ session.Attachable = &RunnerProxy{}

type RunnerProxy struct {
	buildkitdHost string
}

func NewRunnerProxy(buildkitdHost string) *RunnerProxy {
	return &RunnerProxy{
		buildkitdHost: buildkitdHost,
	}
}

func (p *RunnerProxy) Register(server *grpc.Server) {
	sshforward.RegisterSSHServer(server, p)
}

func (p *RunnerProxy) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, nil
}

func (p *RunnerProxy) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	// TODO:(sipsma) also handle basic unix/tcp connections
	ch, err := connhelper.GetConnectionHelper(p.buildkitdHost)
	if err != nil {
		return err
	}

	buildkitdConn, err := ch.ContextDialer(context.TODO(), p.buildkitdHost)
	if err != nil {
		return err
	}
	return sshforward.Copy(context.TODO(), buildkitdConn, stream, nil)
}
