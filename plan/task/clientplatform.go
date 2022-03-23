package task

import (
	"context"
	"runtime"

	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ClientPlatform", func() Task { return &clientPlatformTask{} })
}

type clientPlatformTask struct {
}

func (t clientPlatformTask) GetReference() bkgw.Reference {
	return nil
}

func (t clientPlatformTask) Run(ctx context.Context, pctx *plancontext.Context, _ solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	return compiler.NewValue().FillFields(map[string]interface{}{
		"os":   runtime.GOOS,
		"arch": runtime.GOARCH,
	})
}
