package main

import (
	"context"

	"github.com/dagger/dagger/util/parallel"
)

// Verify that generated code is up to date
func (dev *DaggerDev) CheckGenerated(ctx context.Context) error {
	_, err := dev.Generate(ctx, true)
	return err
}

func (dev *DaggerDev) CheckReleaseDryRun(ctx context.Context) error {
	return parallel.New().
		WithJob("Helm chart", dag.Helm().CheckReleaseDryRun).
		WithJob("CLI", dag.DaggerCli().CheckReleaseDryRun).
		WithJob("Engine", dag.DaggerEngine().CheckReleaseDryRun).
		WithJob("SDKs", func(context.Context) error {
			type dryRunner interface {
				Name() string
				CheckReleaseDryRun(context.Context) error
			}
			jobs := parallel.New()
			for _, sdk := range allSDKs[dryRunner](dev) {
				jobs = jobs.WithJob(sdk.Name(), sdk.CheckReleaseDryRun)
			}
			return jobs.Run(ctx)
		}).
		Run(ctx)
}

func (dev *DaggerDev) CheckLint(ctx context.Context) error {
	return parallel.New().
		WithJob("Go packages", dev.CheckLintGo).
		WithJob("Docs", dev.CheckLintDocs).
		WithJob("Helm chart", dev.CheckLintHelm).
		WithJob("Install scripts", dev.CheckLintScripts).
		WithJob("SDKs", dev.CheckLintSDKs).
		Run(ctx)
}

// Check that go modules have up-to-date go.mod and go.sum
func (dev *DaggerDev) CheckTidy(ctx context.Context) error {
	return dev.godev().CheckTidy(ctx)
}

// Run linters for all SDKs
func (dev *DaggerDev) CheckLintSDKs(ctx context.Context) error {
	type linter interface {
		Name() string
		CheckLint(context.Context) error
	}
	jobs := parallel.New()
	for _, sdk := range allSDKs[linter](dev) {
		jobs = jobs.WithJob(sdk.Name(), sdk.CheckLint)
	}
	return jobs.Run(ctx)
}

// Lint the helm chart
func (dev *DaggerDev) CheckLintHelm(ctx context.Context) error {
	return dag.Helm().CheckLint(ctx)
}

// Lint the documentation
func (dev *DaggerDev) CheckLintDocs(ctx context.Context) error {
	return dag.Docs().CheckLint(ctx)
}

// Lint the install scripts
func (dev *DaggerDev) CheckLintScripts(ctx context.Context) error {
	return dev.Scripts().CheckLint(ctx)
}

// Lint the Go codebase
func (dev *DaggerDev) CheckLintGo(ctx context.Context) error {
	return dev.godev().CheckLint(ctx)
}

// Verify that scripts work correctly
func (dev *DaggerDev) CheckTestScripts(ctx context.Context) error {
	return dev.Scripts().Test(ctx)
}

// Verify that helm works correctly
func (dev *DaggerDev) CheckTestHelm(ctx context.Context) error {
	return dag.Helm().Test(ctx)
}

// Run all checks for all SDKs
func (dev *DaggerDev) CheckTestSDKs(ctx context.Context) error {
	type tester interface {
		Name() string
		Test(context.Context) error
	}
	jobs := parallel.New()
	for _, sdk := range allSDKs[tester](dev) {
		jobs = jobs.WithJob(sdk.Name(), sdk.Test)
	}
	return jobs.Run(ctx)
}
