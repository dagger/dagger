package server

import (
	"context"
	"errors"
	"sync"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session"
	"github.com/moby/buildkit/frontend"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/worker"
)

func NewFrontend(ctx context.Context, w worker.Worker, bkSessionManager *bksession.Manager) (*Frontend, error) {
	sessionManager, err := session.NewManager(ctx, w, bkSessionManager)
	if err != nil {
		return nil, err
	}
	return &Frontend{
		worker:         w,
		sessionManager: sessionManager,
		servers:        map[string]*Server{},
	}, nil
}

type Frontend struct {
	worker         worker.Worker
	sessionManager *session.Manager

	// server id -> server
	servers  map[string]*Server
	serverMu sync.Mutex
}

var _ frontend.Frontend = (*Frontend)(nil)

func (f *Frontend) Solve(
	ctx context.Context,
	llbBridge frontend.FrontendLLBBridge,
	opts map[string]string,
	_ map[string]*pb.Definition,
	frontendSessionID string,
	_ *bksession.Manager,
) (*frontend.Result, error) {
	frontendOpts := &engine.FrontendOpts{}
	if err := frontendOpts.FromSolveOpts(opts); err != nil {
		return nil, err
	}
	if frontendSessionID != f.sessionManager.ID() {
		return nil, errors.New("invalid frontend session id")
	}

	f.serverMu.Lock()
	srv, ok := f.servers[frontendOpts.ServerID]
	if !ok {
		srv = &Server{
			FrontendOpts:     frontendOpts,
			llbBridge:        llbBridge,
			worker:           f.worker,
			sessionManager:   f.sessionManager,
			connectedClients: map[string]bksession.Caller{},
		}
		f.servers[frontendOpts.ServerID] = srv
	}
	f.serverMu.Unlock()

	// TODO: if no more clients connected, delete Server from map
	return srv.Run(ctx)
}
