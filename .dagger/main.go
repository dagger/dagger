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
func (dev *DaggerDev) Generated(ctx context.Context) error {
	generated := dag.CurrentModule().Generators().Run()
	if empty, err := generated.IsEmpty(ctx); err != nil {
		return err
	} else if !empty {
		changes := generated.Changes()
		diffStat, err := changes.DiffStat(ctx)
		if err != nil {
			return err
		}
		entries := make([]patchpreview.Entry, len(diffStat))
		for i, s := range diffStat {
			entries[i] = patchpreview.Entry{
				Path:    s.Path,
				Kind:    s.Kind,
				Added:   s.AddedLines,
				Removed: s.RemovedLines,
			}
		}
		fmt.Fprintln(os.Stderr, patchpreview.SummarizeString(entries, 80))
		return errors.New("generated files are not up-to-date")
	}
	return nil
}
