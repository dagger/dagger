package main

import (
	"context"

	"github.com/dagger/dagger/util/parallel"
)

// Run all linters
func (dev *DaggerDev) Lint(ctx context.Context) (CheckStatus, error) {
	return CheckCompleted, parallel.New().
		WithJob("Go packages", func(ctx context.Context) error {
			_, err := dev.LintGo(ctx)
			return err
		}).
		WithJob("Docs", func(ctx context.Context) error {
			_, err := dev.LintDocs(ctx)
			return err
		}).
		WithJob("Helm chart", func(ctx context.Context) error {
			_, err := dev.LintHelm(ctx)
			return err
		}).
		WithJob("Install scripts", func(ctx context.Context) error {
			_, err := dev.Scripts().Lint(ctx)
			return err
		}).
		WithJob("SDKs", func(ctx context.Context) error {
			_, err := dev.LintSDKs(ctx)
			return err
		}).
		Run(ctx)
}

// "CI in CI": check that Dagger can still run its own CI
// Note: this doesn't actually call all CI checks: only a small subset,
// selected for maximum coverage of Dagger features with limited compute expenditure.
// The actual checks being performed is an implementation detail, and should NOT be relied on.
// In other words, don't skip running <foo> just because it happens to be run here!
func (dev *DaggerDev) CiInCi(ctx context.Context) (CheckStatus, error) {
	ctr, err := dev.Playground(ctx, dev.Source, DistroAlpine, false, false)
	if err != nil {
		return CheckCompleted, err
	}
	ctr = ctr.
		With(dev.withDockerCfg).
		WithMountedDirectory(".git/", dev.Git.Head().Tree().Directory(".git/"))

	_, err = ctr.
		WithExec([]string{"dagger", "call", "test-sdks"}).
		Sync(ctx)
	return CheckCompleted, err
}
