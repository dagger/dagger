// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct{}

// Verify that generated code is up to date
// +check
func (dev *DaggerDev) Generated(ctx context.Context, ws *dagger.Workspace) error {
	generated := ws.Generators().Run()
	changes := generated.Changes()
	rawPatch, err := changes.AsPatch().Contents(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, rawPatch)
	return nil
}
