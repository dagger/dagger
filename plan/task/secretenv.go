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
	Register("SecretEnv", func() Task { return &secretEnvTask{} })
}

type secretEnvTask struct {
}

func (c secretEnvTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	var secretEnv struct {
		Envvar string
	}

	if err := v.Decode(&secretEnv); err != nil {
		return nil, err
	}

	lg.Debug().Str("envvar", secretEnv.Envvar).Msg("loading secret")

	env := os.Getenv(secretEnv.Envvar)
	if env == "" {
		return nil, fmt.Errorf("environment variable %q not set", secretEnv.Envvar)
	}
	id := pctx.Secrets.Register(&plancontext.Secret{
		PlainText: env,
	})

	return compiler.NewValueWithContent(id,
		cue.Str("contents"),
		cue.Str("id"),
	)
}
