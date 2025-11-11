package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
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
		WithJob("Helm chart", dag.Helm().Lint).
		Run(ctx)
}

// Perform a dry run of the release process
func (dev *DaggerDev) ReleaseDryRun(ctx context.Context) (MyCheckStatus, error) {
	return CheckCompleted, parallel.New().
		WithJob("Helm chart", dag.Helm().ReleaseDryRun).
		WithJob("CLI", func(ctx context.Context) error {
			_, err := dag.DaggerCli().ReleaseDryRun(ctx)
			return err
		}).
		WithJob("Engine", dag.EngineDev().ReleaseDryRun).
		WithJob("SDKs", dag.Sdks().ReleaseDryRun).
		Run(ctx)
}

// Lint scripts files
// // TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (s Scripts) Lint(ctx context.Context,
	// +defaultPath="/"
	// +ignore=["*", "!install.sh", "!install.ps1"]
	scripts *dagger.Directory,
) error {
	return parallel.New().
		WithJob("install.sh", func(ctx context.Context) error {
			_, err := s.LintSh(ctx, scripts.File("install.sh"))
			return err
		}).
		WithJob("install.ps1", func(ctx context.Context) error {
			_, err := s.LintPowershell(ctx, scripts.File("install.ps1"))
			return err
		}).
		Run(ctx)
}
