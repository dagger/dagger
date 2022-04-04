package task

import (
	"context"
	"strings"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("TrimSecret", func() Task { return &trimSecretTask{} })
}

type trimSecretTask struct {
}

func (t *trimSecretTask) Run(_ context.Context, pctx *plancontext.Context, _ *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	input, err := pctx.Secrets.FromValue(v.Lookup("input"))
	if err != nil {
		return nil, err
	}

	plaintext := strings.TrimSpace(input.PlainText())
	secret := pctx.Secrets.New(plaintext)

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": secret.MarshalCUE(),
	})
}
