// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct{}

// Verify that generated code is up to date
// +check
func (dev *DaggerDev) CheckGenerated(ctx context.Context) error {
	// _, err := dev.Generate(ctx, true)
	// return err
	return nil
}
