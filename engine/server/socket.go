package server

import (
	"context"
	"fmt"
	"net/url"

	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/sshforward"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type socketProxy struct {
	c                *daggerClient
	bkSessionManager *bksession.Manager
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

	opts, _ := metadata.FromIncomingContext(ctx)
	ctx = metadata.NewOutgoingContext(ctx, opts)

	var connURL *url.URL
	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		var err error
		connURL, err = url.Parse(v[0])
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid id: %s", err)
		}
	}

	var caller bksession.Caller
	var err error
	if connURL != nil && connURL.Fragment != "" {
		sessionID := connURL.Fragment
		caller, err = p.bkSessionManager.Get(ctx, sessionID, true)
		if err != nil {
			return fmt.Errorf("failed to get session: %w", err)
		}
	} else {
		caller, err = p.c.getMainClientCaller()
		if err != nil {
			return fmt.Errorf("failed to get main client caller: %w", err)
		}
	}

	forwardAgentClient, err := sshforward.NewSSHClient(caller.Conn()).ForwardAgent(ctx)
	if err != nil {
		return err
	}
	return proxyStream[sshforward.BytesMessage](ctx, forwardAgentClient, stream)
}
