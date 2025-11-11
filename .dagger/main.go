// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct{}

func (dev *DaggerDev) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
	var (
		bumpDocs, bumpHelm, bumpSDKs *dagger.Changeset
	)
	err := parallel.New().
		WithJob("bump docs version", func(ctx context.Context) error {
			var err error
			bumpDocs, err = dag.DaggerDocs().Bump(version).Sync(ctx)
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
			var err error
			bumpSDKs, err = dag.Sdks().Bump(version).Sync(ctx)
			return err
		}).
		Run(ctx)
	if err != nil {
		return nil, err
	}
	return changesetMerge(bumpSDKs, bumpDocs, bumpHelm), nil
}
