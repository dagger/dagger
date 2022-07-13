package task

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
	"gopkg.in/yaml.v3"
)

func init() {
	Register("DecodeSecret", func() Task { return &decodeSecretTask{} })
}

type decodeSecretTask struct {
}

func (c *decodeSecretTask) Run(ctx context.Context, pctx *plancontext.Context, _ *solver.Solver, v *compiler.Value) (TaskResult, error) {
	lg := log.Ctx(ctx)
	lg.Debug().Msg("decoding secret")

	input := v.Lookup("input")

	inputSecret, err := pctx.Secrets.FromValue(input)
	if err != nil {
		return nil, err
	}

	format, err := v.Lookup("format").String()
	if err != nil {
		return nil, err
	}

	lg.Debug().Str("format", format).Msg("unmarshaling secret")

	inputSecretPlaintext := inputSecret.PlainText()

	var unmarshaled map[string]interface{}

	switch format {
	case "json":
		err = json.Unmarshal([]byte(inputSecretPlaintext), &unmarshaled)
	case "yaml":
		err = yaml.Unmarshal([]byte(inputSecretPlaintext), &unmarshaled)
	}

	if err != nil {
		// returning err here could expose secret plaintext!
		return nil, errors.New("could not unmarshal secret")
	}

	var convert func(i interface{}) interface{}
	convert = func(i interface{}) interface{} {
		switch entry := i.(type) {
		case string:
			secret := pctx.Secrets.New(entry)
			lg.Debug().Str("type", "string").Msg("found secret")
			return secret.MarshalCUE()
		case map[string]interface{}:
			for key, val := range entry {
				entry[key] = convert(val)
			}
			return entry
		}
		return errors.New("invalid type for secret")
	}

	return TaskResult{
		"output": convert(unmarshaled),
	}, nil
}
