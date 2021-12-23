package task

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("InputSecretExec", func() Task { return &inputSecretExecTask{} })
}

type inputSecretExecTask struct {
}

func (c *inputSecretExecTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	var secretExec struct {
		Command struct {
			Name string
			Args []string
		}
		TrimSpace bool
	}

	if err := v.Decode(&secretExec); err != nil {
		return nil, err
	}

	lg := log.Ctx(ctx)
	lg.Debug().Str("name", secretExec.Command.Name).Str("args", strings.Join(secretExec.Command.Args, " ")).Str("trimSpace", fmt.Sprintf("%t", secretExec.TrimSpace)).Msg("loading secret")

	// sec audited by @aluzzardi and @mrjones
	out, err := exec.CommandContext(ctx, secretExec.Command.Name, secretExec.Command.Args...).Output() //#nosec G204
	if err != nil {
		return nil, err
	}

	plaintext := string(out)

	if secretExec.TrimSpace {
		plaintext = strings.TrimSpace(plaintext)
	}

	secret := pctx.Secrets.New(plaintext)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"contents": secret.MarshalCUE(),
	})
}
