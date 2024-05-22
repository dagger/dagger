package main

import (
	"context"
	"fmt"
)

type MyModule struct{}

func (m *MyModule) Build(ctx context.Context, dir *Directory, secret *Secret) (*Container, error) {
	secretName, err := secret.Name(ctx)
	if err != nil {
		return nil, err
	}

	return dir.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Dockerfile: "Dockerfile",
			BuildArgs: []BuildArg{
					{Name: "gh-secret", Value: secretName},
				},
			Secrets:    []*dagger.Secret{secret},
		})
}
