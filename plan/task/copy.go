package task

import (
	"context"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Copy", func() Task { return &copyTask{} })
}

type copyTask struct {
}

func (t *copyTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	var err error

	input, err := pctx.FS.FromValue(v.Lookup("input"))
	if err != nil {
		return nil, err
	}

	inputState, err := input.State()
	if err != nil {
		return nil, err
	}

	contents, err := pctx.FS.FromValue(v.Lookup("contents"))
	if err != nil {
		return nil, err
	}

	contentsState, err := contents.State()
	if err != nil {
		return nil, err
	}

	sourcePath, err := v.Lookup("source").String()
	if err != nil {
		return nil, err
	}

	destPath, err := v.Lookup("dest").String()
	if err != nil {
		return nil, err
	}

	var filters copyFilters

	if err := v.Decode(&filters); err != nil {
		return nil, err
	}

	// FIXME: allow more configurable llb options
	// For now we define the following convenience presets.
	opts := &llb.CopyInfo{
		CopyDirContentsOnly: true,
		CreateDestPath:      true,
		AllowWildcard:       true,
		IncludePatterns:     filters.Include,
		ExcludePatterns:     filters.Exclude,
	}

	outputState := inputState.File(
		llb.Copy(
			contentsState,
			sourcePath,
			destPath,
			opts,
		),
		withCustomName(v, "Copy %s%s %s", sourcePath, filters, destPath),
	)

	result, err := s.Solve(ctx, outputState, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	fs := pctx.FS.New(result)

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": fs.MarshalCUE(),
	})
}

type copyFilters struct {
	Include []string
	Exclude []string
}

// glob like format
// {a/b/c,a/b/d,!ignore}
func (v copyFilters) String() string {
	b := strings.Builder{}

	c := 0

	for i := range v.Include {
		if c > 0 {
			b.WriteString(",")
		}
		b.WriteString(v.Include[i])
		c++
	}

	for i := range v.Exclude {
		if c > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('!')
		b.WriteString(v.Exclude[i])
		c++
	}

	if b.Len() > 0 {
		return "{" + b.String() + "}"
	}

	return ""
}
