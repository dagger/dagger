package main

import (
	"context"
	"os"
	"slices"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

// Verify that generated code is up to date
func (dev *DaggerDev) CheckGenerated(ctx context.Context) (MyCheckStatus, error) {
	_, err := dev.Generate(ctx, true)
	return CheckCompleted, err
}

// Run all code generation - SDKs, docs, grpc stubs, changelog
func (dev *DaggerDev) Generate(ctx context.Context,
	// +optional
	check bool,
) (*dagger.Changeset, error) {
	var genDocs, genEngine, genChangelog, genGHA *dagger.Changeset
	var genSDKs []*dagger.Changeset
	maybeCheck := func(ctx context.Context, changes *dagger.Changeset) error {
		if !check {
			return nil
		}
		return assertNoChanges(ctx, changes, os.Stderr)
	}
	err := parallel.New().
		WithJob("docs", func(ctx context.Context) error {
			var err error
			genDocs, err = dev.Docs().Generate().Sync(ctx)
			if err != nil {
				return err
			}
			return maybeCheck(ctx, genDocs)
		}).
		WithJob("engine", func(ctx context.Context) error {
			var err error
			genEngine, err = dag.DaggerEngine().Generate().Sync(ctx)
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
		WithJob("Github Actions config", func(ctx context.Context) error {
			var err error
			genGHA, err = dag.Ci().Generate().Sync(ctx)
			if err != nil {
				return err
			}
			return maybeCheck(ctx, genGHA)
		}).
		WithJob("SDKs", func(ctx context.Context) error {
			jobs := parallel.New()
			// 1. Builtin SDK toolchains have an eager signature
			type eagerGenerator interface {
				Generate(context.Context) (*dagger.Changeset, error)
			}
			eagerGenerators := allSDKs[eagerGenerator](dev)
			eagerGen := make([]*dagger.Changeset, len(eagerGenerators))
			for i, sdk := range eagerGenerators {
				jobs = jobs.WithJob(sdk.Name, func(ctx context.Context) error {
					gen, err := sdk.Value.Generate(ctx)
					if err != nil {
						return err
					}
					eagerGen[i] = gen
					return maybeCheck(ctx, gen)
				})
			}
			// 2. SDK toolchains in standalone modules have a lazy signature
			type lazyGenerator interface {
				Generate() *dagger.Changeset
			}
			lazyGenerators := allSDKs[lazyGenerator](dev)
			lazyGen := make([]*dagger.Changeset, len(lazyGenerators))
			for i, sdk := range lazyGenerators {
				jobs = jobs.WithJob(sdk.Name, func(ctx context.Context) error {
					gen, err := sdk.Value.Generate().Sync(ctx)
					if err != nil {
						return err
					}
					lazyGen[i] = gen
					return maybeCheck(ctx, gen)
				})
			}
			// 3. Run all jobs and collect results
			if err := jobs.Run(ctx); err != nil {
				return err
			}
			genSDKs = append(genSDKs, eagerGen...)
			genSDKs = append(genSDKs, lazyGen...)
			return nil
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
		changes := slices.Clone(genSDKs)
		changes = append(changes, genDocs, genEngine, genChangelog, genGHA)
		result, err = changesetMerge(changes...).Sync(ctx)
		return err
	})
	return result, err
}
