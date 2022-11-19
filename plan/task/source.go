package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Source", func() Task { return &sourceTask{} })
}

type sourceTask struct {
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

func (c *sourceTask) Run(ctx context.Context, pctx *plancontext.Context, _ *solver.Solver, dgr *dagger.Client, v *compiler.Value) (*compiler.Value, error) {
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

	dirId, err := dgr.Host().Directory(path, dagger.HostDirectoryOpts{
		Include: source.Include,
		Exclude: source.Exclude,
	}).ID(ctx)
	if err != nil {
		return nil, err
	}

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": utils.NewFS(dirId),
	})
}
