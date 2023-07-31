package buildkit

import (
	"context"
	"errors"
	"io"

	"github.com/moby/buildkit/session/sshforward"
	"github.com/moby/buildkit/util/bklog"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// TODO: could make generic func to reduce tons of below boilerplate

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

	incomingMD, _ := metadata.FromIncomingContext(ctx)
	ctx = metadata.NewOutgoingContext(ctx, incomingMD)

	forwardAgentClient, err := sshforward.NewSSHClient(p.c.MainClientCaller.Conn()).ForwardAgent(ctx)
	if err != nil {
		return err
	}

	var eg errgroup.Group
	var done bool
	eg.Go(func() (rerr error) {
		defer func() {
			bklog.G(ctx).WithError(rerr).Debug("forward agent receiver done")
			if rerr == io.EOF {
				rerr = nil
			}
			if errors.Is(rerr, context.Canceled) && done {
				rerr = nil
			}
			if rerr != nil {
				cancel()
			}
		}()
		for {
			pkt, err := withContext(ctx, func() (*sshforward.BytesMessage, error) {
				var pkt sshforward.BytesMessage
				err := stream.RecvMsg(&pkt)
				return &pkt, err
			})
			if err != nil {
				return err
			}
			if err := forwardAgentClient.SendMsg(pkt); err != nil {
				return err
			}
		}
	})
	eg.Go(func() (rerr error) {
		defer func() {
			bklog.G(ctx).WithError(rerr).Debug("forward agent sender done")
			if rerr == io.EOF {
				rerr = nil
			}
			if rerr == nil {
				done = true
			}
			cancel()
		}()
		for {
			pkt, err := withContext(ctx, func() (*sshforward.BytesMessage, error) {
				var pkt sshforward.BytesMessage
				err := forwardAgentClient.RecvMsg(&pkt)
				return &pkt, err
			})
			if err != nil {
				return err
			}
			if err := stream.SendMsg(pkt); err != nil {
				return err
			}
		}
	})
	return eg.Wait()
}
