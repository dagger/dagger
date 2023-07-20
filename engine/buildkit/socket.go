package buildkit

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/moby/buildkit/session/sshforward"
	"github.com/moby/buildkit/util/bklog"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

// TODO: could make generic func to reduce tons of below boilerplate

type socketProxy struct {
	c *Client
}

func (p *socketProxy) Register(srv *grpc.Server) {
	sshforward.RegisterSSHServer(srv, p)
}

func (p *socketProxy) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	data, err := p.c.getSocketDataFromID(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return sshforward.NewSSHClient(data.session.Conn()).CheckAgent(ctx, req)
}

func (p *socketProxy) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	ctx, baseData, err := p.c.GetSessionResourceData(stream)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	opts := baseData.socketData
	if opts == nil {
		return fmt.Errorf("expected socket opts")
	}
	forwardAgentClient, err := sshforward.NewSSHClient(opts.session.Conn()).ForwardAgent(ctx)
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
