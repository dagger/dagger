package task

import (
	"context"
	"io/fs"

	"cuelang.org/go/cue"
	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Mkdir", func() Task { return &mkdirTask{} })
}

type mkdirTask struct{}

func (t *mkdirTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	path, err := v.Lookup("path").String()
	if err != nil {
		return nil, err
	}

	// Permissions (int)
	permissions, err := v.Lookup("permissions").Int64()
	if err != nil {
		return nil, err
	}

	// Retrieve options
	mkdirOpts := []llb.MkdirOption{}
	var opts struct {
		Parents bool
	}

	if err := v.Decode(&opts); err != nil {
		return nil, err
	}

	if opts.Parents {
		mkdirOpts = append(mkdirOpts, llb.WithParents(true))
	}

	// Retrieve input Filesystem
	input, err := pctx.FS.FromValue(v.Lookup("input"))
	if err != nil {
		return nil, err
	}

	// Retrieve input llb state
	inputState, err := input.State()
	if err != nil {
		return nil, err
	}

	// Add Mkdir operation on input llb state
	outputState := inputState.File(
		llb.Mkdir(path, fs.FileMode(permissions), mkdirOpts...),
		withCustomName(v, "Mkdir %s", path),
	)

	// Compute state
	result, err := s.Solve(ctx, outputState, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	// Retrieve result result filesystem
	outputFS := pctx.FS.New(result)

	// Init output
	output := compiler.NewValue()

	if err := output.FillPath(cue.ParsePath("output"), outputFS.MarshalCUE()); err != nil {
		return nil, err
	}
	return output, nil
}
