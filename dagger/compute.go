package dagger

import (
	"context"
	"fmt"

	cueerrors "cuelang.org/go/cue/errors"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
)

// Buildkit compute entrypoint (BK calls if "solve" or "build")
// Use by wrapping in a buildkit client Build call, or buildkit frontend.
func Compute(ctx context.Context, c bkgw.Client) (r *bkgw.Result, err error) {
	// FIXME: wrap errors to avoid crashing buildkit Build()
	//    with cue error types (why??)
	defer func() {
		if err != nil {
			err = fmt.Errorf("%s", cueerrors.Details(err, nil))
			debugf("execute returned an error. Wrapping...")
		}
	}()
	debugf("initializing env")
	env, err := NewEnv(ctx, c)
	if err != nil {
		return nil, err
	}
	debugf("computing env")
	// Compute output overlay
	if err := env.Compute(ctx); err != nil {
		return nil, err
	}
	debugf("exporting env")
	// Export env to a cue directory
	outdir := NewSolver(c).Scratch()
	outdir, err = env.Export(outdir)
	if err != nil {
		return nil, err
	}
	// Wrap cue directory in buildkit result
	return outdir.Result(ctx)
}
