package task

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("TrimSecret", func() Task { return &trimSecretTask{} })
}

type trimSecretTask struct {
}

func (t *trimSecretTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, client *dagger.Client, v *compiler.Value) (*compiler.Value, error) {
	// input, err := pctx.Secrets.FromValue(v.Lookup("input"))
	secretid, err := utils.GetSecretId(v.Lookup("input"))
	if err != nil {
		return nil, err
	}

	plaintext, err := client.Secret(secretid).Plaintext(ctx)
	if err != nil {
		return nil, err
	}

	plaintext = strings.TrimSpace(plaintext)
	newsecretid, err := s.NewSecret(plaintext).ID(ctx)
	if err != nil {
		return nil, err
	}
	// secret := pctx.Secrets.New(plaintext)

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": utils.NewSecretFromId(newsecretid),
	})
}
