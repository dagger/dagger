package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Source", func() Task { return &sourceTask{} })
}

type sourceTask struct {
}

func (c *sourceTask) GetReference() bkgw.Reference {
	return nil
}

func (c *sourceTask) PreRun(ctx context.Context, pctx *plancontext.Context, v *compiler.Value) error {
	origPath, err := v.Lookup("path").String()
	if err != nil {
		return err
	}

	absPath, err := v.Lookup("path").AbsPath()
	if err != nil {
		return err
	}

	location, err := v.Lookup("path").Dirname()
	if err != nil {
		return err
	}

	if !strings.HasPrefix(absPath, location) {
		return fmt.Errorf("path %q is not relative", origPath)
	}

	if _, err := os.Stat(absPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("path %q does not exist", origPath)
	}

	pctx.LocalDirs.Add(absPath)

	return nil
}

func (c *sourceTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	path, err := v.Lookup("path").AbsPath()
	if err != nil {
		return nil, err
	}

	var source struct {
		Include []string
		Exclude []string
	}

	if err := v.Decode(&source); err != nil {
		return nil, err
	}

	lg.Debug().Str("path", path).Msg("loading local directory")
	opts := []llb.LocalOption{
		withCustomName(v, "Embed %s", path),
		llb.IncludePatterns(source.Include),
		llb.ExcludePatterns(source.Exclude),
		// Without hint, multiple `llb.Local` operations on the
		// same path get a different digest.
		llb.SessionID(s.SessionID()),
		llb.SharedKeyHint(path),
	}

	// FIXME: Remove the `Copy` and use `Local` directly.
	//
	// Copy'ing is a costly operation which should be unnecessary.
	// However, using llb.Local directly breaks caching sometimes for unknown reasons.
	st := llb.Scratch().File(
		llb.Copy(
			llb.Local(
				path,
				opts...,
			),
			"/",
			"/",
		),
		withCustomName(v, "Embed %s [copy]", path),
	)

	result, err := s.Solve(ctx, st, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	fs := pctx.FS.New(result)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": fs.MarshalCUE(),
	})
}
