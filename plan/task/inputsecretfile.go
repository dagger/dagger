package task

import (
	"context"
	"os"
	"strings"

	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("InputSecretFile", func() Task { return &inputSecretFileTask{} })
}

type inputSecretFileTask struct {
}

func (c *inputSecretFileTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	path, err := v.Lookup("path").AbsPath()
	if err != nil {
		return nil, err
	}

	lg.Debug().Str("path", path).Msg("loading secret")

	fileBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	plaintext := string(fileBytes)
	trimSpace, err := v.Lookup("trimSpace").Bool()
	if err != nil {
		return nil, err
	}
	if trimSpace {
		plaintext = strings.TrimSpace(plaintext)
	}

	secret := pctx.Secrets.New(plaintext)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"contents": secret.MarshalCUE(),
	})
}
