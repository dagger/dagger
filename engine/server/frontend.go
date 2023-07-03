package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/dagger/dagger/engine/session"
	"github.com/moby/buildkit/frontend"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/worker"
)

const (
	DaggerFrontendName    = "dagger.v0"
	daggerFrontendOptsKey = "dagger_frontend_opts"
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
	frontendOpts := &FrontendOpts{}
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

type FrontendOpts struct {
	ServerID         string            `json:"server_id,omitempty"`
	ClientSessionID  string            `json:"client_session_id,omitempty"`
	CacheConfigType  string            `json:"cache_config_type,omitempty"`
	CacheConfigAttrs map[string]string `json:"cache_config_attrs,omitempty"`
}

func (f FrontendOpts) ServerAddr() string {
	return fmt.Sprintf("unix://%s", f.ServerSockPath())
}

func (f FrontendOpts) ServerSockPath() string {
	return fmt.Sprintf("/run/dagger/server-%s.sock", f.ServerID)
}

func (f *FrontendOpts) FromSolveOpts(opts map[string]string) error {
	strVal, ok := opts[daggerFrontendOptsKey]
	if !ok {
		return nil
	}
	err := json.Unmarshal([]byte(strVal), f)
	if err != nil {
		return err
	}
	if f.ServerID == "" {
		return errors.New("missing server id from frontend opts")
	}
	return nil
}

func (f FrontendOpts) ToSolveOpts() (map[string]string, error) {
	if f.ServerID == "" {
		return nil, errors.New("missing server id from frontend opts")
	}
	b, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		daggerFrontendOptsKey: string(b),
	}, nil
}
