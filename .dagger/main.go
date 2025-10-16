// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
	"github.com/dagger/dagger/util/parallel"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct {
	Source *dagger.Directory // +private

	Version string
	Tag     string
	Git     *dagger.GitRepository

	// Can be used by nested clients to forward docker credentials to avoid
	// rate limits
	DockerCfg *dagger.Secret // +private
}

func New(
	ctx context.Context,
	// +optional
	// +defaultPath="/"
	// +ignore=[
	// "bin",
	// ".git",
	// "**/node_modules",
	// "**/.venv",
	// "**/__pycache__",
	// "docs/node_modules",
	// "sdk/typescript/node_modules",
	// "sdk/typescript/dist",
	// "sdk/rust/examples/backend/target",
	// "sdk/rust/target",
	// "sdk/php/vendor"
	// ]
	source *dagger.Directory,

	// +defaultPath="/"
	git *dagger.GitRepository,

	// +optional
	dockerCfg *dagger.Secret,
) (*DaggerDev, error) {
	v := dag.Version()
	version, err := v.Version(ctx)
	if err != nil {
		return nil, err
	}
	tag, err := v.ImageTag(ctx)
	if err != nil {
		return nil, err
	}

	dev := &DaggerDev{
		Source:    source,
		Tag:       tag,
		Git:       git,
		Version:   version,
		DockerCfg: dockerCfg,
	}
	return dev, nil
}

func (dev *DaggerDev) withDockerCfg(ctr *dagger.Container) *dagger.Container {
	if dev.DockerCfg == nil {
		return ctr
	}
	return ctr.WithMountedSecret("/root/.docker/config.json", dev.DockerCfg)
}

func (dev *DaggerDev) Bump(ctx context.Context, version string) (*dagger.Changeset, error) {
	var (
		bumpDocs, bumpHelm *dagger.Changeset
		bumpSDKs           []*dagger.Changeset
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
	return changesetMerge(append(bumpSDKs, bumpDocs, bumpHelm)...), nil
}
