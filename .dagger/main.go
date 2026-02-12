// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/dagger/dagger/util/patchpreview"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct{}

// Verify that generated code is up to date
// +check
func (dev *DaggerDev) CheckGenerated(ctx context.Context) error {
	generated := dag.CurrentModule().Generators().Run()
	if empty, err := generated.IsEmpty(ctx); err != nil {
		return err
	} else if !empty {
		changes := generated.Changes()
		rawPatch, err := changes.AsPatch().Contents(ctx)
		if err != nil {
			return err
		}
		preview, err := patchpreview.SummarizeString(ctx, rawPatch, changes)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, preview)
		return errors.New("generated files are not up-to-date")
	}
	return nil
}
