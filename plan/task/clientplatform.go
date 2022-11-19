package task

import (
	"context"
	"runtime"

	"dagger.io/dagger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ClientPlatform", func() Task { return &clientPlatformTask{} })
}

type clientPlatformTask struct {
}

func (t clientPlatformTask) Run(_ context.Context, _ *plancontext.Context, _ *solver.Solver, _ *dagger.Client, _ *compiler.Value) (*compiler.Value, error) {
	return compiler.NewValue().FillFields(map[string]interface{}{
		"os":   runtime.GOOS,
		"arch": runtime.GOARCH,
	})
}
