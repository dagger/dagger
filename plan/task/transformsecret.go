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
	Register("TransformSecret", func() Task { return &transformSecretTask{} })
}

type transformSecretTask struct {
}

func (c *transformSecretTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)
	lg.Debug().Msg("transforming secret")

	input := v.Lookup("input")
	if !plancontext.IsSecretValue(input) {
		return nil, errors.New("#TransformSecret requires input: #Secret")
	}

	inputSecret, err := pctx.Secrets.FromValue(input)
	if err != nil {
		return nil, err
	}

	function := v.Lookup("#function")
	function.FillPath(cue.ParsePath("input"), inputSecret.PlainText())

	outputPlaintext, err := function.Lookup("output").String()
	if err != nil {
		return nil, err
	}

	outputSecret := pctx.Secrets.New(outputPlaintext)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": outputSecret.MarshalCUE(),
	})
}
