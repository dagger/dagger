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
		ReleaseDryRun() MyCheckStatus
	}
	jobs := parallel.New()
	for _, sdk := range allSDKs[releaseDryRunner](dev) {
		jobs = jobs.WithJob(sdk.Name, func(ctx context.Context) error {
			_, err := sdk.Value.ReleaseDryRun(ctx)
			return err
		})
	}
	return jobs.Run(ctx)
}

// Run all checks for all SDKs
// TODO: remove after merging https://github.com/dagger/dagger/pull/11211
func (sdks *AllSdks) TestSDKs(ctx context.Context) error {
	return parallel.New().
		WithJob("go", dag.GoSDKDev().Test)

	jobs := parallel.New()
	type tester interface {
		Test(context.Context) error
	}
	for _, sdk := range allSDKs[tester](dev) {
		jobs = jobs.WithJob(sdk.Name, func(ctx context.Context) error {
			_, err := sdk.Value.Test(ctx)
			return err
		})
	}
	// Some (but not all) sdk test functions are also aggregators which will be replaced by PR 11211. Call them here too.
	type deprecatedTester interface {
		Test(context.Context) error
	}
	for _, sdk := range allSDKs[deprecatedTester](dev) {
		jobs = jobs.WithJob(sdk.Name, sdk.Value.Test)
	}
	return jobs.Run(ctx)
}
