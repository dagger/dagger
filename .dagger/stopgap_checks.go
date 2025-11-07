package main

import (
	"context"

	"github.com/dagger/dagger/util/parallel"
)

// Lint docs, helm chart and install scripts
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (dev *DaggerDev) LintMisc(ctx context.Context) error {
	return parallel.New().
		WithJob("Docs", func(ctx context.Context) error {
			return dev.Docs().Lint(ctx)
		}).
		WithJob("Helm chart", func(ctx context.Context) error {
			return dev.LintHelm(ctx)
		}).
		WithJob("Install scripts", dev.Scripts().Lint).
		Run(ctx)
}

// DryRun performs a dry run of the release process
// +check
func (dev *DaggerDev) ReleaseDryRun(ctx context.Context) error {
	return parallel.New().
		WithJob("Helm chart", func(ctx context.Context) error {
			return dag.Helm().ReleaseDryRun(ctx)
		}).
		WithJob("CLI", func(ctx context.Context) error {
			return dag.DaggerCli().ReleaseDryRun(ctx)
		}).
		WithJob("Engine", func(ctx context.Context) error {
			return dag.DaggerEngine().ReleaseDryRun(ctx)
		}).
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
		jobs = jobs.WithJob(sdk.Name(), func(ctx context.Context) error {
			return sdk.Test(ctx)
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
		// +check
		Lint(context.Context) error
	}
	for _, sdk := range allSDKs[linter](dev) {
		jobs = jobs.WithJob(sdk.Name(), func(ctx context.Context) error {
			return sdk.Lint(ctx)
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
			return t.TestNode(ctx)
		}).
		WithJob("bun", func(ctx context.Context) error {
			return t.TestBun(ctx)
		}).
		Run(ctx)
}

// Lint the Rust SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (r RustSDK) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("check format", func(ctx context.Context) error {
			return r.CheckFormat(ctx)
		}).
		WithJob("check compilation", func(ctx context.Context) error {
			return r.CheckCompilation(ctx)
		}).
		Run(ctx)
}

// Lint scripts files
// // TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (s Scripts) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("install.sh", func(ctx context.Context) error {
			return s.LintSh(ctx)
		}).
		WithJob("install.ps1", func(ctx context.Context) error {
			return s.LintPowershell(ctx)
		}).
		Run(ctx)
}

// Lint the Typescript SDK
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (t TypescriptSDK) Lint(ctx context.Context) error {
	return parallel.New().
		WithJob("typescript", func(ctx context.Context) error {
			return t.LintTypescript(ctx)
		}).
		WithJob("docs snippets", func(ctx context.Context) error {
			return t.LintDocsSnippets(ctx)
		}).
		Run(ctx)
}
