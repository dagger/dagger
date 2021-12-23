package task

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("LoadSecret", func() Task { return &loadSecretTask{} })
}

type loadSecretTask struct {
}

func (t *loadSecretTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	path, err := v.Lookup("path").String()
	if err != nil {
		return nil, err
	}

	input, err := pctx.FS.FromValue(v.Lookup("input"))
	if err != nil {
		return nil, err
	}
	inputFS := solver.NewBuildkitFS(input.Result())

	// FIXME: we should create an intermediate image containing only `path`.
	// That way, on cache misses, we'll only download the layer with the file contents rather than the entire FS.
	contents, err := fs.ReadFile(inputFS, path)
	if err != nil {
		return nil, fmt.Errorf("ReadFile %s: %w", path, err)
	}
	plaintext := string(contents)

	trimSpace, err := v.Lookup("trimSpace").Bool()
	if err != nil {
		return nil, err
	}
	if trimSpace {
		plaintext = strings.TrimSpace(plaintext)
	}

	secret := pctx.Secrets.New(plaintext)

	return compiler.NewValue().FillFields(map[string]interface{}{
		"contents": secret.MarshalCUE(),
	})
}
