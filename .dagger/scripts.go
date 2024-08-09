package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
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
			Check(s.Dagger.Source().File("install.ps1"), dagger.PsAnalyzerCheckOpts{
				// Exclude the unused parameters for now due because PSScriptAnalyzer treat
				// parameters in `Install-Dagger` as unused but the script won't run if we delete
				// it.
				ExcludeRules: []string{"PSReviewUnusedParameter"},
			}).
			Assert(ctx)
	})
	return eg.Wait()
}
