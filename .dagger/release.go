package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

func (dev *DaggerDev) ReleaseDryRun(ctx context.Context) (CheckStatus, error) {
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
		WithJob("SDKs", func(ctx context.Context) error {
			_, err := dev.releaseDryRunSDKs(ctx)
			return err
		}).
		Run(ctx)
}

// Bump the version of all versioned components
func (dev *DaggerDev) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
	var (
		bumpDocs, bumpHelm *dagger.Changeset
		bumpSDKs           []*dagger.Changeset
	)
	err := parallel.New().
		WithJob("bump docs version", func(ctx context.Context) error {
			var err error
			bumpDocs, err = dag.Docs().Bump(version).Sync(ctx)
			return err
		}).
		WithJob("bump helm chart version", func(ctx context.Context) error {
			chartYaml, err := dag.Helm().SetVersion(version).Sync(ctx)
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
			type bumper interface {
				Bump(context.Context, string) (*dagger.Changeset, error)
				Name() string
			}
			bumpers := allSDKs[bumper](dev)
			bumpSDKs = make([]*dagger.Changeset, len(bumpers))
			for i, sdk := range bumpers {
				bumped, err := sdk.Bump(ctx, version)
				if err != nil {
					return err
				}
				bumped, err = bumped.Sync(ctx)
				if err != nil {
					return err
				}
				bumpSDKs[i] = bumped
			}
			return nil
		}).
		Run(ctx)
	if err != nil {
		return nil, err
	}
	return changesetMerge(dev.Source, append(bumpSDKs, bumpDocs, bumpHelm)...), nil
}
