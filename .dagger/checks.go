package main

import (
	"context"

	"github.com/dagger/dagger/util/parallel"
)

// Verify that generated code is up to date
func (dev *DaggerDev) CheckGenerated(ctx context.Context) (CheckStatus, error) {
	_, err := dev.Generate(ctx, true)
	return CheckCompleted, err
}

func (dev *DaggerDev) ReleaseDryRun(ctx context.Context) (CheckStatus, error) {
	return CheckCompleted, parallel.New().
		WithJob("Helm chart", func(ctx context.Context) error {
			_, err := dag.Helm().ReleaseDryRun(ctx)
			return err
		}).
		WithJob("CLI", func(ctx context.Context) error {
			_, err := dag.DaggerCli().ReleaseDryRun(ctx)
			return err
		}).
		WithJob("Engine", func(ctx context.Context) error {
			_, err := dag.DaggerEngine().ReleaseDryRun(ctx)
			return err
		}).
		WithJob("SDKs", func(ctx context.Context) error {
			_, err := dev.releaseDryRunSDKs(ctx)
			return err
		}).
		Run(ctx)
}

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
	ctr, err := dev.Dev(ctx, dev.Source, DistroAlpine, false, false)
	if err != nil {
		return CheckCompleted, err
	}
	ctr = ctr.WithMountedDirectory(".git/", dev.Git.Head().Tree().Directory(".git/"))

	_, err = ctr.
		WithExec([]string{"dagger", "call", "test-sdks"}).
		Sync(ctx)
	return CheckCompleted, err
}

// Check that go modules have up-to-date go.mod and go.sum
func (dev *DaggerDev) CheckTidy(ctx context.Context) (CheckStatus, error) {
	_, err := dev.godev().CheckTidy(ctx)
	return CheckCompleted, err
}

// Lint the helm chart
func (dev *DaggerDev) LintHelm(ctx context.Context) (CheckStatus, error) {
	_, err := dag.Helm().Lint(ctx)
	return CheckCompleted, err
}

// Lint the documentation
func (dev *DaggerDev) LintDocs(ctx context.Context) (CheckStatus, error) {
	_, err := dag.Docs().Lint(ctx)
	return CheckCompleted, err
}

// Lint the Go codebase
func (dev *DaggerDev) LintGo(ctx context.Context) (CheckStatus, error) {
	_, err := dev.godev().Lint(ctx)
	return CheckCompleted, err
}

// Verify that helm works correctly
func (dev *DaggerDev) TestHelm(ctx context.Context) (CheckStatus, error) {
	_, err := dag.Helm().Test(ctx)
	return CheckCompleted, err
}
