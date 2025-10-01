package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

// Verify that generated code is up to date
func (dev *DaggerDev) CheckGenerated(ctx context.Context) error {
	gen, err := dev.Generate(ctx)
	if err != nil {
		return err
	}
	summary, err := changesetSummary(ctx, gen)
	if err != nil {
		return err
	}
	if len(summary) > 0 {
		return fmt.Errorf("generated files are not up-to-date")
	}
	return nil
}

func (dev *DaggerDev) CheckReleaseDryRun(ctx context.Context) error {
	return parallel.New().
		WithJob("Helm chart", dag.Helm().CheckReleaseDryRun).
		WithJob("CLI", dag.DaggerCli().CheckReleaseDryRun).
		WithJob("Engine", dag.DaggerCli().CheckReleaseDryRun).
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
		WithJob("lint go packages", func(ctx context.Context) error {
			return dev.CheckGoLint(ctx, nil)
		}).
		WithJob("lint docs", dag.Docs().CheckLint).
		WithJob("lint helm chart", dag.Helm().CheckLint).
		WithJob("lint install scripts", dev.Scripts().CheckLint).
		WithJob("lint SDKs", func(ctx context.Context) error {
			type linter interface {
				Name() string
				CheckLint(context.Context) error
			}
			jobs := parallel.New()
			for _, sdk := range allSDKs[linter](dev) {
				jobs = jobs.WithJob(sdk.Name(), sdk.CheckLint)
			}
			return jobs.Run(ctx)
		}).
		Run(ctx)
}

// Lint the Go codebase
func (dev *DaggerDev) CheckGoLint(
	ctx context.Context,
	pkgs []string, // +optional
) error {
	if len(pkgs) == 0 {
		allPkgs, err := dev.containing(ctx, "go.mod")
		if err != nil {
			return err
		}
		for _, pkg := range allPkgs {
			if strings.HasPrefix(pkg, "docs/") {
				continue
			}
			if strings.HasPrefix(pkg, "core/integration/") {
				continue
			}
			if strings.HasPrefix(pkg, "dagql/idtui/viztest/broken") {
				continue
			}
			if strings.HasPrefix(pkg, "modules/claude/") {
				// re-enable after we ship its dependent APIs
				continue
			}
			if strings.HasPrefix(pkg, "modules/evals/") {
				// re-enable after we ship its dependent APIs
				continue
			}
			pkgs = append(pkgs, pkg)
		}
	}
	return dag.
		Go(dev.SourceDeveloped()).
		Lint(ctx, dagger.GoLintOpts{Packages: pkgs})
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
