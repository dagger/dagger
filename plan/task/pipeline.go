package task

import (
	"context"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/environment"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("#up", func() Task { return &pipelineTask{} })
}

// pipelineTask is an adapter for legacy pipelines (`#up`).
// FIXME: remove once fully migrated to Europa.
type pipelineTask struct {
}

func (c *pipelineTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	p := environment.NewPipeline(v, s, pctx)
	if err := p.Run(ctx); err != nil {
		return nil, err
	}
	return p.Computed(), nil
}
