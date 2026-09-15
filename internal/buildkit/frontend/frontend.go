package frontend

import (
	"context"

	"github.com/dagger/dagger/internal/buildkit/client/llb/sourceresolver"
	"github.com/dagger/dagger/internal/buildkit/executor"
	gw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/solver"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/solver/result"
	digest "github.com/opencontainers/go-digest"
)

type Result = result.Result[solver.ResultProxy]

type Attestation = result.Attestation[solver.ResultProxy]

type Frontend interface {
	Solve(ctx context.Context, llb FrontendLLBBridge, exec executor.Executor, opt map[string]string, inputs map[string]*pb.Definition, sid string, sm *session.Manager) (*Result, error)
}

type FrontendLLBBridge interface {
	sourceresolver.MetaResolver
	Solve(ctx context.Context, req SolveRequest, sid string) (*Result, error)
	Warn(ctx context.Context, dgst digest.Digest, msg string, opts WarnOpts) error
}

type SolveRequest = gw.SolveRequest

type CacheOptionsEntry = gw.CacheOptionsEntry

type WarnOpts = gw.WarnOpts
