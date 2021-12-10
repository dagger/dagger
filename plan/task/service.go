package task

import (
	"context"
	"errors"

	"cuelang.org/go/cue"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Service", func() Task { return &serviceTask{} })
}

type serviceTask struct {
}

func (c serviceTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	unix, _ := v.LookupPath(cue.ParsePath("unix")).String()
	npipe, _ := v.LookupPath(cue.ParsePath("npipe")).String()

	if unix == "" && npipe == "" {
		return nil, errors.New("invalid service")
	}

	lg := log.Ctx(ctx).Debug()

	if unix != "" {
		lg.Str("unix", unix)
	} else if npipe != "" {
		lg.Str("npipe", npipe)
	}

	lg.Msg("loading service")

	service := pctx.Services.New(unix, npipe)
	out := compiler.NewValue()
	if err := out.FillPath(cue.ParsePath("service"), service.MarshalCUE()); err != nil {
		return nil, err
	}
	return out, nil
}
