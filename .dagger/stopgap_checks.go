package main

import (
	"context"

	"github.com/dagger/dagger/util/parallel"
)

// This file contains temporary code, to be removed once 'dagger checks' is merged and released.
type MyCheckStatus string

const (
	CheckCompleted MyCheckStatus = "COMPLETED"
	CheckSkipped   MyCheckStatus = "SKIPPED"
)

// Lint docs, helm chart and install scripts
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (dev *DaggerDev) LintMisc(ctx context.Context) error {
	return parallel.New().
		WithJob("Docs", func(ctx context.Context) error {
			_, err := dev.Docs().Lint(ctx)
			return err
		}).
		WithJob("Helm chart", func(ctx context.Context) error {
			_, err := dev.LintHelm(ctx)
			return err
		}).
		WithJob("Install scripts", dev.Scripts().Lint).
		Run(ctx)
}

// DryRun performs a dry run of the release process
func (dev *DaggerDev) ReleaseDryRun(ctx context.Context) (MyCheckStatus, error) {
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
		WithJob("SDKs", dev.dryRunSDKs).
		Run(ctx)
}

func (dev *DaggerDev) dryRunSDKs(ctx context.Context) error {
	type releaseDryRunner interface {
		Name() string
		ReleaseDryRun(context.Context) (MyCheckStatus, error)
	}
	jobs := parallel.New()
	for _, sdk := range allSDKs[releaseDryRunner](dev) {
		jobs = jobs.WithJob(sdk.Name(), func(ctx context.Context) error {
			_, err := sdk.ReleaseDryRun(ctx)
			return err
		})
	}
	return jobs.Run(ctx)
}

// Run all checks for all SDKs
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (dev *DaggerDev) TestSDKs(ctx context.Context) error {
	jobs := parallel.New()
	type tester interface {
		Name() string
		Test(context.Context) (MyCheckStatus, error)
	}
	for _, sdk := range allSDKs[tester](dev) {
		jobs = jobs.WithJob(sdk.Name(), func(ctx context.Context) error {
			_, err := sdk.Test(ctx)
			return err
		})
	}
	// Some (but not all) sdk test functions are also aggregators which will be replaced by PR 11211. Call them here too.
	type deprecatedTester interface {
		Name() string
		Test(context.Context) error
	}
	for _, sdk := range allSDKs[deprecatedTester](dev) {
		jobs = jobs.WithJob(sdk.Name(), sdk.Test)
	}
	return jobs.Run(ctx)
}

// Run linters for all SDKs
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (dev *DaggerDev) LintSDKs(ctx context.Context) error {
	jobs := parallel.New()
	type linter interface {
		Name() string
		Lint(context.Context) (MyCheckStatus, error)
	}
	for _, sdk := range allSDKs[linter](dev) {
		jobs = jobs.WithJob(sdk.Name(), func(ctx context.Context) error {
			_, err := sdk.Lint(ctx)
			return err
		})
	}
	// Some (but not all) sdk lint functions are also aggregators which will be replaced by PR 11211. Call them here too.
	type deprecatedLinter interface {
		Name() string
		Lint(context.Context) error
	}
	for _, sdk := range allSDKs[deprecatedLinter](dev) {
		jobs = jobs.WithJob(sdk.Name(), sdk.Lint)
	}
	return jobs.Run(ctx)
}

// Test the Typescript SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (t TypescriptSDK) Test(ctx context.Context) error {
	return parallel.New().
		WithJob("node", func(ctx context.Context) error {
			_, err := t.TestNode(ctx)
			return err
		}).
		WithJob("bun", func(ctx context.Context) error {
			_, err := t.TestBun(ctx)
			return err
		}).
		Run(ctx)
}

// Lint the Rust SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (r RustSDK) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("check format", func(ctx context.Context) error {
			_, err := r.CheckFormat(ctx)
			return err
		}).
		WithJob("check compilation", func(ctx context.Context) error {
			_, err := r.CheckCompilation(ctx)
			return err
		}).
		Run(ctx)
}

// Lint scripts files
// // TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (s Scripts) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("install.sh", func(ctx context.Context) error {
			_, err := s.LintSh(ctx)
			return err
		}).
		WithJob("install.ps1", func(ctx context.Context) error {
			_, err := s.LintPowershell(ctx)
			return err
		}).
		Run(ctx)
}

// Lint the Typescript SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (t TypescriptSDK) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("typescript", func(ctx context.Context) error {
			_, err := t.LintTypescript(ctx)
			return err
		}).
		WithJob("docs snippets", func(ctx context.Context) error {
			_, err := t.LintDocsSnippets(ctx)
			return err
		}).
		Run(ctx)
}
