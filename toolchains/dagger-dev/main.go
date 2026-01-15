// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"
	"os"

	"dagger/dagger-dev/internal/dagger"

	"github.com/dagger/dagger/util/parallel"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct{}

// Verify that generated code is up to date
// +check
func (dev *DaggerDev) Check(ctx context.Context) error {
	_, err := dev.All(ctx, true)
	return err
}

// Run all code generation - SDKs, docs, grpc stubs, changelog
func (dev *DaggerDev) All(ctx context.Context,
	// +optional
	check bool,
) (*dagger.Changeset, error) {
	var genDocs, genEngine, genChangelog, genSDKs *dagger.Changeset
	maybeCheck := func(ctx context.Context, changes *dagger.Changeset) error {
		if !check {
			return nil
		}
		return assertNoChanges(ctx, changes, os.Stderr)
	}
	err := parallel.New().
		WithJob("docs", func(ctx context.Context) error {
			var err error
			genDocs, err = dag.Docs().Generate().Sync(ctx)
			if err != nil {
				return err
			}
			return maybeCheck(ctx, genDocs)
		}).
		WithJob("engine", func(ctx context.Context) error {
			var err error
			genEngine, err = dag.EngineDev().Generate().Sync(ctx)
			if err != nil {
				return err
			}
			return maybeCheck(ctx, genEngine)
		}).
		WithJob("changelog", func(ctx context.Context) error {
			var err error
			genChangelog, err = dag.Changelog().Generate().Sync(ctx)
			if err != nil {
				return err
			}
			return maybeCheck(ctx, genChangelog)
		}).
		WithJob("SDKs", func(ctx context.Context) error {
			var err error
			genSDKs, err = dag.Sdks().Generate().Sync(ctx)
			if err != nil {
				return err
			}
			return maybeCheck(ctx, genSDKs)
		}).
		Run(ctx)
	if err != nil {
		return nil, err
	}
	if check {
		return nil, nil
	}
	var result *dagger.Changeset
	// FIXME: this is a workaround to TUI being too noisy
	err = parallel.Run(ctx, "merge all changesets", func(ctx context.Context) error {
		var err error
		var gen []*dagger.Changeset
		gen = append(gen, genDocs, genEngine, genChangelog, genSDKs)
		result, err = changesetMerge(gen...).Sync(ctx)
		return err
	})
	return result, err
}
