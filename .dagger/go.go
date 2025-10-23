package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// Lint the Go codebase
func (dev *DaggerDev) LintGo(ctx context.Context) (CheckStatus, error) {
	_, err := dev.godev().Lint(ctx)
	return CheckCompleted, err
}

// Check that go modules have up-to-date go.mod and go.sum
func (dev *DaggerDev) CheckTidy(ctx context.Context) (CheckStatus, error) {
	_, err := dev.godev().CheckTidy(ctx)
	return CheckCompleted, err
}

func (dev *DaggerDev) godev() *dagger.Go {
	return dag.Go(dev.Source, dagger.GoOpts{
		// FIXME: differentiate between:
		// 1) lint exclusions,
		// 2) go mod tidy exclusions,
		// 3) dagger runtime generation exclusions
		// 4) actually building & testing stuff
		// --> maybe it's a "check exclusion"?
		Exclude: []string{
			"docs/**",
			"core/integration/**",
			"dagql/idtui/viztest/broken/**",
			"modules/evals/**",
			"**/broken*/**",
		},
		Values: []string{
			"github.com/dagger/dagger/engine.Version=" + dev.Version,
			"github.com/dagger/dagger/engine.Tag=" + dev.Tag,
		},
	})
}
