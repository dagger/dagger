package task

import (
	"context"
	"encoding/json"
	"errors"

	"cuelang.org/go/cue"
	"dagger.io/dagger"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
	"gopkg.in/yaml.v3"
)

func init() {
	Register("DecodeSecret", func() Task { return &decodeSecretTask{} })
}

type decodeSecretTask struct {
}

func (c *decodeSecretTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, client *dagger.Client, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)
	lg.Debug().Msg("decoding secret")

	input := v.Lookup("input")
	secretid, err := utils.GetSecretId(input)
	if err != nil {
		return nil, err
	}
	inputSecret := client.Secret(secretid)

	format, err := v.Lookup("format").String()
	if err != nil {
		return nil, err
	}

	lg.Debug().Str("format", format).Msg("unmarshaling secret")

	inputSecretPlaintext, err := inputSecret.Plaintext(ctx)
	if err != nil {
		return nil, err
	}

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

	output := compiler.NewValue()

	// recurse over unmarshaled to convert string values to secrets
	var convert func(p []cue.Selector, i interface{})
	convert = func(p []cue.Selector, i interface{}) {
		switch entry := i.(type) {
		case string:
			secretid, err := s.NewSecret(entry).ID(ctx)
			if err != nil {
				panic(err)
			}

			secret := utils.NewSecretFromId(secretid)
			// secret := pctx.Secrets.New(entry)
			p = append(p, cue.ParsePath("contents").Selectors()...)
			logPath := cue.MakePath(p[1 : len(p)-1]...)
			lg.Debug().Str("path", logPath.String()).Str("type", "string").Msg("found secret")
			path := cue.MakePath(p...)
			output.FillPath(path, secret)
		case map[string]interface{}:
			for k, v := range entry {
				np := append([]cue.Selector{}, p...)
				np = append(np, cue.ParsePath(k).Selectors()...)
				convert(np, v)
			}
		}
	}

	convert(cue.ParsePath("output").Selectors(), unmarshaled)

	return output, nil
}
