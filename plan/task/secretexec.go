package task

import (
	"context"
	"fmt"
	"os/exec"

	// "gopkg.in/yaml.v3"
	"encoding/json"

	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("SecretExec", func() Task { return &secretExecTask{} })
}

type secretExecTask struct {
}

func (c secretExecTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	var secretExec struct {
		Command string
		Format  string
		Args    []string
	}

	if err := v.Decode(&secretExec); err != nil {
		return nil, err
	}

	lg.Debug().Str("command", secretExec.Command).Msg("executing secret command")

	out, err := exec.Command(secretExec.Command, secretExec.Args...).Output()
	if err != nil {
		return nil, err
	}

	switch secretExec.Format {
	case "json":
		lg.Debug().Str("format", secretExec.Format).Msg("unmarshaling command output")
		cueMap := make(map[string]interface{})
		jsonStruct := make(map[string]interface{})
		err := json.Unmarshal(out, &jsonStruct)
		if err != nil {
			return nil, err
		}
		for k, v := range jsonStruct {
			secret := pctx.Secrets.New(fmt.Sprintf("%s", v))
			cueMap[k] = secret.MarshalCUE()
		}
		return compiler.NewValue().FillFields(map[string]interface{}{
			"contents": cueMap,
		})
		// case "yaml":

		// case "text":
	}

	return nil, nil
}
