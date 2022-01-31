package task

import (
	"context"
	"errors"
	"strings"

	"cuelang.org/go/cue"
	"github.com/rs/zerolog/log"
	"github.com/sergi/go-diff/diffmatchpatch"
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

	inputSecret, err := pctx.Secrets.FromValue(input)
	if err != nil {
		return nil, err
	}

	// "copy" function value to new empty value to avoid data race associated with v.Fill during a task
	function := compiler.NewValue()
	err = function.FillPath(cue.MakePath(), v.Lookup("#function"))
	if err != nil {
		return nil, err
	}

	inputSecretPlaintext := inputSecret.PlainText()
	err = function.FillPath(cue.ParsePath("input"), inputSecretPlaintext)
	if err != nil {
		dmp := diffmatchpatch.New()
		errStr := err.Error()
		diffs := dmp.DiffMain(inputSecretPlaintext, err.Error(), false)
		for _, diff := range diffs {
			if diff.Type == diffmatchpatch.DiffEqual {
				// diffText := strings.ReplaceAll(diff.Text, ":", "") // colons are tricky. Yaml keys end with them but if a secret contained one that got replaced, the secret wouldn't get redacted
				errStr = strings.ReplaceAll(errStr, diff.Text, "***")
			}
		}

		return nil, errors.New(errStr)
	}

	type pathSecret struct {
		path   cue.Path
		secret *plancontext.Secret
	}

	var pathsSecrets []pathSecret

	// users could yaml.Unmarshal(input) and return a map
	// or yaml.Unmarshal(input).someKey and return a string
	// walk will ensure we convert every leaf
	functionPathSelectors := function.Path().Selectors()
	function.Lookup("output").Walk(nil, func(v *compiler.Value) {
		if v.Kind() == cue.StringKind {
			plaintext, _ := v.String()
			secret := pctx.Secrets.New(plaintext)
			newLeafSelectors := v.Path().Selectors()[len(functionPathSelectors):]
			newLeafSelectors = append(newLeafSelectors, cue.Str("contents"))
			newLeafPath := cue.MakePath(newLeafSelectors...)
			pathsSecrets = append(pathsSecrets, pathSecret{newLeafPath, secret})
		}
	})

	output := compiler.NewValue()

	// use FillPath outside of Walk to avoid data race
	for _, ps := range pathsSecrets {
		output.FillPath(ps.path, ps.secret.MarshalCUE())
	}

	return output, nil
}
