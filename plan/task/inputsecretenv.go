package task

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("InputSecretEnv", func() Task { return &inputSecretEnvTask{} })
}

type inputSecretEnvTask struct {
}

func (c *inputSecretEnvTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	var secretEnv struct {
		Envvar    string
		TrimSpace bool
	}

	if err := v.Decode(&secretEnv); err != nil {
		return nil, err
	}

	lg.Debug().Str("envvar", secretEnv.Envvar).Str("trimSpace", fmt.Sprintf("%t", secretEnv.TrimSpace)).Msg("loading secret")

	env := os.Getenv(secretEnv.Envvar)
	if env == "" {
		return nil, fmt.Errorf("environment variable %q not set", secretEnv.Envvar)
	}

	if secretEnv.TrimSpace {
		env = strings.TrimSpace(env)
	}

	secret := pctx.Secrets.New(env)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"contents": secret.MarshalCUE(),
	})
}
