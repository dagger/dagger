package task

import (
	"context"

	"cuelang.org/go/cue"
	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Import", func() Task { return &importTask{} })
}

type importTask struct {
}

func (c importTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	var dir struct {
		Path    string
		Include []string
		Exclude []string
	}

	if err := v.Decode(&dir); err != nil {
		return nil, err
	}

	opts := []llb.LocalOption{
		withCustomName(v, "Local %s", dir.Path),
		// Without hint, multiple `llb.Local` operations on the
		// same path get a different digest.
		llb.SessionID(s.SessionID()),
		llb.SharedKeyHint(dir.Path),
	}

	if len(dir.Include) > 0 {
		opts = append(opts, llb.IncludePatterns(dir.Include))
	}

	// Excludes .dagger directory by default
	excludePatterns := []string{"**/.dagger/"}
	if len(dir.Exclude) > 0 {
		excludePatterns = dir.Exclude
	}
	opts = append(opts, llb.ExcludePatterns(excludePatterns))

	// FIXME: Remove the `Copy` and use `Local` directly.
	//
	// Copy'ing is a costly operation which should be unnecessary.
	// However, using llb.Local directly breaks caching sometimes for unknown reasons.
	st := llb.Scratch().File(
		llb.Copy(
			llb.Local(
				dir.Path,
				opts...,
			),
			"/",
			"/",
		),
		withCustomName(v, "Local %s [copy]", dir.Path),
	)

	result, err := s.Solve(ctx, st, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	id := pctx.FS.Register(&plancontext.FS{
		Result: result,
	})

	return compiler.NewValueWithContent(id,
		cue.Str("fs"),
		cue.Hid("_fs", "alpha.dagger.io/dagger"),
		cue.Str("id"),
	)
}
