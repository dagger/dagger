package main

import (
	"context"

	"github.com/dagger/dagger/util/parallel"
)

// Lint docs, helm chart and install scripts
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (dev *DaggerDev) LintMisc(ctx context.Context) error {
	return parallel.New().
		WithJob("Docs", dev.Docs().Lint).
		WithJob("Helm chart", dev.LintHelm).
		WithJob("Install scripts", dev.Scripts().Lint).
		Run(ctx)
}

// DryRun performs a dry run of the release process
// +check
func (dev *DaggerDev) ReleaseDryRun(ctx context.Context) error {
	return parallel.New().
		WithJob("Helm chart", dag.Helm().ReleaseDryRun).
		WithJob("CLI", dag.DaggerCli().ReleaseDryRun).
		WithJob("Engine", dag.DaggerEngine().ReleaseDryRun).
		WithJob("SDKs", dev.dryRunSDKs).
		Run(ctx)
}

func (dev *DaggerDev) dryRunSDKs(ctx context.Context) error {
	type releaseDryRunner interface {
		Name() string
		// +check
		ReleaseDryRun(context.Context) error
	}
	jobs := parallel.New()
	for _, sdk := range allSDKs[releaseDryRunner](dev) {
		jobs = jobs.WithJob(sdk.Name(), func(ctx context.Context) error {
			return sdk.ReleaseDryRun(ctx)
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
		// +check
		Test(context.Context) error
	}
	for _, sdk := range allSDKs[tester](dev) {
		jobs = jobs.WithJob(sdk.Name(), sdk.Test)
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
		// +check
		Lint(context.Context) error
	}
	for _, sdk := range allSDKs[linter](dev) {
		jobs = jobs.WithJob(sdk.Name(), sdk.Lint)
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
		WithJob("node", t.TestNode).
		WithJob("bun", t.TestBun).
		Run(ctx)
}

// Lint the Rust SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (r RustSDK) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("check format", r.CheckFormat).
		WithJob("check compilation", r.CheckCompilation).
		Run(ctx)
}

// Lint scripts files
// // TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (s Scripts) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("install.sh", s.LintSh).
		WithJob("install.ps1", s.LintPowershell).
		Run(ctx)
}

// Lint the Typescript SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (t TypescriptSDK) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("typescript", t.LintTypescript).
		WithJob("docs snippets", t.LintDocsSnippets).
		Run(ctx)
}
