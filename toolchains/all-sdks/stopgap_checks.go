package main

import (
	"context"
	"dagger/all-sdks/internal/dagger"

	"github.com/dagger/dagger/util/parallel"
)

// Merge Changesets together
// FIXME: move this to core dagger: https://github.com/dagger/dagger/issues/11189
func changesetMerge(changesets ...*dagger.Changeset) *dagger.Changeset {
	before := dag.Directory()
	for _, changeset := range changesets {
		before = before.WithDirectory("", changeset.Before())
	}
	after := before
	for _, changeset := range changesets {
		after = after.WithChanges(changeset)
	}
	return after.Changes(before)
}

func (sdks *AllSdks) ReleaseDryRun(ctx context.Context) error {
	type releaseDryRunner interface {
		ReleaseDryRun(context.Context) error
	}
	jobs := parallel.New()
	for _, sdk := range all[releaseDryRunner]() {
		jobs = jobs.WithJob(sdk.Name, sdk.Value.ReleaseDryRun)
	}
	return jobs.Run(ctx)
}

// Run all checks for all SDKs
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (sdks *AllSdks) Test(ctx context.Context) error {
	jobs := parallel.New()
	type tester interface {
		Test(context.Context) error
	}
	for _, sdk := range all[tester]() {
		jobs = jobs.WithJob(sdk.Name, sdk.Value.Test)
	}
	return jobs.Run(ctx)
}

func (sdks *AllSdks) Lint(ctx context.Context) error {
	jobs := parallel.New()
	type linter interface {
		Lint(context.Context) error
	}
	for _, sdk := range all[linter]() {
		jobs = jobs.WithJob(sdk.Name, sdk.Value.Lint)
	}
	return jobs.Run(ctx)
}
