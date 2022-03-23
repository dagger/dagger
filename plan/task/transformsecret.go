package task

import (
	"context"
	"errors"
	"strings"

	"cuelang.org/go/cue"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
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

func (c *transformSecretTask) GetReference() bkgw.Reference {
	return nil
}

func (c *transformSecretTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)
	lg.Debug().Msg("transforming secret")

	input := v.Lookup("input")

	inputSecret, err := pctx.Secrets.FromValue(input)
	if err != nil {
		return nil, err
	}

	function := v.Lookup("#function")
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

	pathToSecrets := make(map[string]string)

	// users could yaml.Unmarshal(input) and return a map
	// or yaml.Unmarshal(input).someKey and return a string
	// walk will ensure we convert every leaf
	functionPathSelectors := function.Path().Selectors()

	function.Lookup("output").Walk(nil, func(v *compiler.Value) {
		if v.Kind() == cue.StringKind {
			plaintext, _ := v.String()
			newLeafSelectors := v.Path().Selectors()[len(functionPathSelectors):]
			newLeafSelectors = append(newLeafSelectors, cue.Str("contents"))
			newLeafPath := cue.MakePath(newLeafSelectors...)
			p := newLeafPath.String()
			pathToSecrets[p] = plaintext
		}
	})

	output := compiler.NewValue()

	for p, s := range pathToSecrets {
		secret := pctx.Secrets.New(s)
		output.FillPath(cue.ParsePath(p), secret.MarshalCUE())
	}

	return output, nil
}
