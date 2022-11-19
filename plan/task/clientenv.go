package task

import (
	"context"
	"fmt"
	"os"

	"cuelang.org/go/cue"
	"dagger.io/dagger"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ClientEnv", func() Task { return &clientEnvTask{} })
}

type clientEnvTask struct {
}

func (t clientEnvTask) Run(ctx context.Context, pctx *plancontext.Context, _ *solver.Solver, client *dagger.Client, v *compiler.Value) (*compiler.Value, error) {
	log.Ctx(ctx).Debug().Msg("loading environment variables")

	fields, err := v.Fields(cue.Optional(true))
	if err != nil {
		return nil, err
	}

	envs := make(map[string]interface{})
	for _, field := range fields {
		if field.Selector == cue.Str("$dagger") {
			continue
		}
		envvar := field.Label()

		val, hasDefault := field.Value.Default()

		env, hasEnv := os.LookupEnv(envvar)

		switch {
		case !hasEnv && !field.IsOptional && !hasDefault:
			return nil, fmt.Errorf("environment variable %q not set", envvar)
		case utils.IsSecretValue(val):
			{
				secretid, err := client.Host().EnvVariable(envvar).Secret().ID(ctx)
				if err != nil {
					return nil, err
				}
				envs[envvar] = utils.NewSecretFromId(secretid)
			}
		case !hasDefault && val.IsConcrete():
			return nil, fmt.Errorf("%s: unexpected concrete value, please use a type or set a default", envvar)
		case val.IncompleteKind() == cue.StringKind:
			envs[envvar] = env
		default:
			return nil, fmt.Errorf("%s: unsupported type %q", envvar, val.IncompleteKind())
		}

	}

	return compiler.NewValue().FillFields(envs)
}
