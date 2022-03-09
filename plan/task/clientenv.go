package task

import (
	"context"
	"fmt"
	"os"

	"cuelang.org/go/cue"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ClientEnv", func() Task { return &clientEnvTask{} })
}

type clientEnvTask struct {
}

func (t clientEnvTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	log.Ctx(ctx).Debug().Msg("loading environment variables")

	fields, err := v.Fields()
	if err != nil {
		return nil, err
	}

	envs := make(map[string]interface{})
	for _, field := range fields {
		if field.Selector == cue.Str("$dagger") {
			continue
		}
		envvar := field.Label()
		val, err := t.getEnv(envvar, field.Value, pctx)
		if err != nil {
			return nil, err
		}
		envs[envvar] = val
	}

	return compiler.NewValue().FillFields(envs)
}

func (t clientEnvTask) getEnv(envvar string, v *compiler.Value, pctx *plancontext.Context) (interface{}, error) {
	env := os.Getenv(envvar)
	if env == "" {
		return nil, fmt.Errorf("environment variable %q not set", envvar)
	}

	// Resolve default in disjunction if a type hasn't been specified
	val, _ := v.Default()

	if plancontext.IsSecretValue(val) {
		secret := pctx.Secrets.New(env)
		return secret.MarshalCUE(), nil
	}

	if val.IsConcrete() {
		return nil, fmt.Errorf("%s: unexpected concrete value, please use a type", envvar)
	}

	k := val.IncompleteKind()
	if k == cue.StringKind {
		return env, nil
	}

	return nil, fmt.Errorf("%s: unsupported type %q", envvar, k)
}
