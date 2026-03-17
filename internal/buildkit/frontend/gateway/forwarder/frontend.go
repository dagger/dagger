package forwarder

import (
	"context"

	"github.com/dagger/dagger/internal/buildkit/executor"
	"github.com/dagger/dagger/internal/buildkit/frontend"
	"github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/worker"
)

func NewGatewayForwarder(w worker.Infos, f client.BuildFunc) frontend.Frontend {
	return &GatewayForwarder{
		workers: w,
		f:       f,
	}
}

type GatewayForwarder struct {
	workers worker.Infos
	f       client.BuildFunc
}

func (gf *GatewayForwarder) Solve(ctx context.Context, llbBridge frontend.FrontendLLBBridge, exec executor.Executor, opts map[string]string, inputs map[string]*pb.Definition, sid string, sm *session.Manager) (retRes *frontend.Result, retErr error) {
	c, err := LLBBridgeToGatewayClient(ctx, llbBridge, exec, opts, inputs, gf.workers, sid, sm)
	if err != nil {
		return nil, err
	}

	defer func() {
		c.discard(retErr)
	}()

	res, err := gf.f(ctx, c)
	if err != nil {
		return nil, err
	}

	return c.toFrontendResult(res)
}
