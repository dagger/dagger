package llbsolver

import (
	"context"

	cacheconfig "github.com/dagger/dagger/internal/buildkit/cache/config"
	"github.com/dagger/dagger/internal/buildkit/frontend"
	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/solver"
	"github.com/dagger/dagger/internal/buildkit/solver/llbsolver/provenance"
	"github.com/dagger/dagger/internal/buildkit/worker"
	"github.com/pkg/errors"
)

type Result struct {
	*frontend.Result
	Provenance *provenance.Result
}

type Attestation = frontend.Attestation

func workerRefResolver(refCfg cacheconfig.RefConfig, all bool, g session.Group) func(ctx context.Context, res solver.Result) ([]*solver.Remote, error) {
	return func(ctx context.Context, res solver.Result) ([]*solver.Remote, error) {
		ref, ok := res.Sys().(*worker.WorkerRef)
		if !ok {
			return nil, errors.Errorf("invalid result: %T", res.Sys())
		}

		return ref.GetRemotes(ctx, true, refCfg, all, g)
	}
}
