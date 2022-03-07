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
	Register("client.env.*", func() Task { return &clientEnvTask{} })
}

type clientEnvTask struct {
}

func (t clientEnvTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	envvar := v.ParentLabel(1)

	lg.Debug().Str("envvar", envvar).Msg("loading environment variable")

	env := os.Getenv(envvar)
	if env == "" {
		return nil, fmt.Errorf("environment variable %q not set", envvar)
	}

	// Resolve default in disjunction if a type hasn't been specified
	val, _ := v.Default()
	out := compiler.NewValue()

	if plancontext.IsSecretValue(val) {
		secret := pctx.Secrets.New(env)
		return out.Fill(secret.MarshalCUE())
	}

	if val.IsConcrete() {
		return nil, fmt.Errorf("unexpected concrete value, please use a type")
	}

	k := val.IncompleteKind()
	if k == cue.StringKind {
		return out.Fill(env)
	}

	return nil, fmt.Errorf("unsupported type %q", k)
}
