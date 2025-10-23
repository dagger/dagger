// Everything you need to develop the Dagger Engine
// https://dagger.io
package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// A dev environment for the DaggerDev Engine
type DaggerDev struct {
	Source *dagger.Directory

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
