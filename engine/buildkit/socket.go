package buildkit

import (
	"context"

	"github.com/moby/buildkit/session/sshforward"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type socketProxy struct {
	c *Client
}

func (p *socketProxy) Register(srv *grpc.Server) {
	sshforward.RegisterSSHServer(srv, p)
}

func (p *socketProxy) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	// NOTE: we currently just fail only at the ForwardAgent call since that's the only time it's currently possible
	// to get the client ID. Not as ideal, but can be improved w/ work to support socket sharing across nested clients.
	return &sshforward.CheckAgentResponse{}, nil
}

func (p *socketProxy) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()

	ctx = trace.ContextWithSpanContext(ctx, p.c.spanCtx) // ensure server's span context is propagated

	incomingMD, _ := metadata.FromIncomingContext(ctx)
	ctx = metadata.NewOutgoingContext(ctx, incomingMD)

	forwardAgentClient, err := sshforward.NewSSHClient(p.c.MainClientCaller.Conn()).ForwardAgent(ctx)
	if err != nil {
		return err
	}
	return proxyStream[sshforward.BytesMessage](ctx, forwardAgentClient, stream)
}
