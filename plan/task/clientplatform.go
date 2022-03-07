package task

import (
	"context"
	"runtime"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ClientPlatform", func() Task { return &clientPlatformTask{} })
}

type clientPlatformTask struct {
}

func (t clientPlatformTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	return compiler.NewValue().FillFields(map[string]interface{}{
		"os":   runtime.GOOS,
		"arch": runtime.GOARCH,
	})
}
