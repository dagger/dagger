package dagger

import (
	"context"
	"fmt"

	cueerrors "cuelang.org/go/cue/errors"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/rs/zerolog/log"
)

// Buildkit compute entrypoint (BK calls if "solve" or "build")
// Use by wrapping in a buildkit client Build call, or buildkit frontend.
func Compute(ctx context.Context, c bkgw.Client) (r *bkgw.Result, err error) {
	lg := log.Ctx(ctx)
	// FIXME: wrap errors to avoid crashing buildkit Build()
	//    with cue error types (why??)
	defer func() {
		if err != nil {
			err = fmt.Errorf("%s", cueerrors.Details(err, nil))
		}
	}()

	s := NewSolver(c)
	// Retrieve updater script form client
	var updater interface{}
	if o, exists := c.BuildOpts().Opts[bkUpdaterKey]; exists {
		updater = o
	}
	env, err := NewEnv()
	if err != nil {
		return nil, err
	}
	if err := env.SetUpdater(updater); err != nil {
		return nil, err
	}
	if err := env.Update(ctx, s); err != nil {
		return nil, err
	}
	if input, exists := c.BuildOpts().Opts["input"]; exists {
		if err := env.SetInput(input); err != nil {
			return nil, err
		}
	}
	lg.Debug().Msg("computing env")
	// Compute output overlay
	if err := env.Compute(ctx, s); err != nil {
		return nil, err
	}
	lg.Debug().Msg("exporting env")
	// Export env to a cue directory
	outdir, err := env.Export(s.Scratch())
	if err != nil {
		return nil, err
	}
	// Wrap cue directory in buildkit result
	return outdir.Result(ctx)
}
