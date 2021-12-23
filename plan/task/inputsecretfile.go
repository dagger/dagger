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
	Register("InputSecretFile", func() Task { return &inputSecretFileTask{} })
}

type inputSecretFileTask struct {
}

func (c *inputSecretFileTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	var secretFile struct {
		Path      string
		TrimSpace bool
	}

	if err := v.Decode(&secretFile); err != nil {
		return nil, err
	}

	lg := log.Ctx(ctx)
	lg.Debug().Str("path", secretFile.Path).Str("trimSpace", fmt.Sprintf("%t", secretFile.TrimSpace)).Msg("loading secret")

	fileBytes, err := os.ReadFile(secretFile.Path)
	if err != nil {
		return nil, err
	}

	plaintext := string(fileBytes)
	if secretFile.TrimSpace {
		plaintext = strings.TrimSpace(plaintext)
	}

	secret := pctx.Secrets.New(plaintext)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"contents": secret.MarshalCUE(),
	})
}
