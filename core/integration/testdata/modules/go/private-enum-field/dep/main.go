package main

import (
	"context"
	"dagger/dep/internal/dagger"
)

type Dep struct {
	Opts []dagger.ContainerPublishOpts // +private
}

func New() *Dep {
	return &Dep{
		Opts: []dagger.ContainerPublishOpts{
			{PlatformVariants: []*dagger.Container{dag.Container().From("alpine")}},
		},
	}
}

func (m *Dep) Publish(ctx context.Context) (string, error) {
	// dry run a publish
	return "registry/repo:latest", nil
}
