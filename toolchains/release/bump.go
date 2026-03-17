package main

import (
	"context"
	"toolchains/release/internal/dagger"

	"github.com/dagger/dagger/util/parallel"
)

// Change the required dagger engine version across all components
func (r *Release) Bump(
	ctx context.Context,
	// The new required engine version
	engineVersion string,
) (*dagger.Changeset, error) {
	var (
		bumpDocs, bumpHelm, bumpSDKs *dagger.Changeset
	)
	err := parallel.New().
		WithJob("bump docs version", func(ctx context.Context) error {
			var err error
			bumpDocs, err = dag.DocsDev().Bump(engineVersion).Sync(ctx)
			return err
		}).
		WithJob("bump helm chart version", func(ctx context.Context) error {
			chartYaml, err := dag.HelmDev().SetVersion(engineVersion).Sync(ctx)
			if err != nil {
				return err
			}
			bumpHelm, err = dag.Directory().
				WithFile("helm/dagger/Chart.yaml", chartYaml).
				Changes(dag.Directory()).
				Sync(ctx)
			return err
		}).
		WithJob("bump SDK versions", func(ctx context.Context) error {
			var err error
			bumpSDKs, err = dag.Sdks().Bump(engineVersion).Sync(ctx)
			return err
		}).
		Run(ctx)
	if err != nil {
		return nil, err
	}
	return changesetMerge(bumpSDKs, bumpDocs, bumpHelm), nil
}

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
