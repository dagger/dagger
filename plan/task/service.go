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
	var unix, npipe string
	var stringErr error

	unixV := v.Lookup("unix")
	npipeV := v.Lookup("npipe")

	if unixV.Exists() && unixV.IsConcrete() {
		unix, stringErr = unixV.String()
	}

	if npipeV.Exists() && npipeV.IsConcrete() {
		npipe, stringErr = npipeV.String()
	}

	if stringErr != nil {
		return nil, stringErr
	}

	if unix == "" && npipe == "" {
		return nil, errors.New("invalid service")
	}

	lg := log.Ctx(ctx)

	if unix != "" {
		lg.Debug().Str("unix", unix).Msg("loading service")
	} else if npipe != "" {
		lg.Debug().Str("npipe", npipe).Msg("loading service")
	}

	service := pctx.Services.New(unix, npipe)
	out := compiler.NewValue()
	if err := out.FillPath(cue.ParsePath("service"), service.MarshalCUE()); err != nil {
		return nil, err
	}
	return out, nil
}
