package main

import (
	"context"

	"golang.org/x/sync/errgroup"
)

type Scripts struct {
	Dagger *DaggerDev // +private
}

// Lint scripts files
func (s Scripts) Lint(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return dag.Shellcheck().
			Check(s.Dagger.Source().File("install.sh")).
			Assert(ctx)
	})
	eg.Go(func() error {
		return dag.PsAnalyzer().
			Check(s.Dagger.Source().File("install.ps1")).
			Assert(ctx)
	})
	return eg.Wait()
}
